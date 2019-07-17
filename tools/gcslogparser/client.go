/*
Copyright 2019 The Knative Authors

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"sync"
	"time"

	"github.com/knative/test-infra/shared/prow"
)

type Parser struct {
	StartDate time.Time             // Earliest date to be analyzed, i.e. "2019-02-22"
	logParser func(s string) string // logParser function
	jobFilter []string              // Jobs to be parsed. If not provided will parse all jobs
	PrChan    chan prInfo           // For PR use only, make it here so it's easier to cleanup
	jobChan   chan prow.Job
	buildChan chan buildInfo
	wgPR      sync.WaitGroup
	wgJob     sync.WaitGroup
	wgBuild   sync.WaitGroup

	found     [][]string
	processed []string

	buildIDChan chan int // keeps the highest buildID which started before StartDate

	mutex         *sync.Mutex
	buildIDMutext *sync.Mutex

	start time.Time
}

type buildInfo struct {
	job prow.Job
	ID  int
}

// Check to see if the buildID is older than currently found old buildID
func (c *Parser) buildIDTooOld(buildID int) bool {
	c.buildIDMutext.Lock()
	defer c.buildIDMutext.Unlock()
	tooOld := <-c.buildIDChan
	res := (buildID < tooOld)
	go func() { c.buildIDChan <- tooOld }()
	// log.Printf("build ID %d %v too old", buildID, res)
	return res
}

func (c *Parser) updateBuildIDChan(newVal int, comp func(int) bool) bool {
	var res bool
	c.buildIDMutext.Lock()
	defer c.buildIDMutext.Unlock()
	tooOld := <-c.buildIDChan
	if comp(tooOld) {
		tooOld = newVal
		res = true
	}
	go func() { c.buildIDChan <- tooOld }()
	return res
}

func NewParser(serviceAccount string) (*Parser, error) {
	if err := prow.Initialize(serviceAccount); nil != err { // Explicit authenticate with gcs Parser
		return nil, fmt.Errorf("Failed authenticating GCS: '%v'", err)
	}

	c := &Parser{}
	c.start = time.Now()
	c.mutex = &sync.Mutex{}
	c.buildIDMutext = &sync.Mutex{}

	c.PrChan = make(chan prInfo, 1000)
	c.jobChan = make(chan prow.Job, 1000)
	c.buildChan = make(chan buildInfo, 10000)

	c.buildIDChan = make(chan int)
	go func() { c.buildIDChan <- 1 }()

	for i := 0; i < 1000; i++ {
		go c.jobListener()
	}
	for i := 0; i < 1000; i++ {
		go c.buildListener()
	}

	return c, nil
}

func (c *Parser) wait() {
	c.wgPR.Wait()
	c.wgJob.Wait()
	c.wgBuild.Wait()
}

func (c *Parser) setStartDate(startDate string) error {
	tt, err := time.Parse("2006-01-02", startDate)
	if nil != err {
		return fmt.Errorf("invalid start date string, expecing format YYYY-MM-DD: '%v'", err)
	}
	c.StartDate = tt
	return nil
}

func (c *Parser) feedSingleBuild(j prow.Job, buildID int) {
	if isTooOld := c.buildIDTooOld(buildID); isTooOld {
		// log.Printf("skipping %d %s", buildID, j.StoragePath)
		return
	}
	c.wgBuild.Add(1)
	c.buildChan <- buildInfo{
		job: j,
		ID:  buildID,
	}
}

func (c *Parser) jobListener() {
	for {
		select {
		case j := <-c.jobChan:
			// First pass, select 1 out of every 50 runs, so the base line of tooold buildID
			// can be quickly formed to avoid GCS operations on too old runs.
			// exclude 0 for first pass as PR jobs may only contain 1 run, don't let it jump too fast
			for index, buildID := range j.GetBuildIDs() {
				if index%50 == 0 && index != 0 {
					c.feedSingleBuild(j, buildID)
				}
			}
			// Sleep 10 seconds so baseline is established
			time.Sleep(10 * time.Second)
			for index, buildID := range j.GetBuildIDs() {
				if index == 0 || index%50 != 0 {
					c.feedSingleBuild(j, buildID)
				}
			}
			c.wgJob.Done()
		}
	}
}

func (c *Parser) buildListener() {
	for {
		select {
		case b := <-c.buildChan:
			if isTooOld := c.buildIDTooOld(b.ID); !isTooOld {
				start := time.Now()
				build := b.job.NewBuild(b.ID)
				log.Println("Initialize: ", b.ID, b.job.StoragePath, "took: ", time.Since(start))
				if build.FinishTime != nil {
					if *build.FinishTime > c.StartDate.Unix() {
						start = time.Now()
						// content, _ := build.ReadFile("build-log.txt")
						output, err := exec.Command("gsutil", "cat", "gs://knative-prow/"+build.GetBuildLogPath()).CombinedOutput()
						if err != nil {
							log.Fatalf("Error downloading: '%s' -err: '%v' '%v'", build.GetBuildLogPath(), string(output), err)
						}
						log.Println("Read file: ", b.ID, b.job.StoragePath, "took: ", time.Since(start))
						if isTooOld = c.buildIDTooOld(b.ID); !isTooOld {
							start = time.Now()
							found := c.logParser(string(output))
							c.mutex.Lock()
							c.processed = append(c.processed, build.StoragePath)
							if "" != found {
								c.found = append(c.found, []string{found, time.Unix(*build.StartTime, 0).String(), build.StoragePath})
							}
							c.mutex.Unlock()
							// log.Println("Parsing: ", b.ID, b.job.StoragePath, "took: ", time.Since(start))
						}
					} else {
						c.updateBuildIDChan(b.ID, func(tooOld int) bool {
							return b.ID > tooOld
						})
					}
				}
			}
			c.wgBuild.Done()
		}
	}
}

func (c *Parser) cleanup() {
	if c.PrChan != nil {
		close(c.PrChan)
	}
	if c.jobChan != nil {
		close(c.jobChan)
	}
	if c.buildChan != nil {
		close(c.buildChan)
	}
	if c.buildIDChan != nil {
		close(c.buildIDChan)
	}

	log.Println("Client lived: ", time.Now().Sub(c.start))
}

// CleanupOnInterrupt will execute the function cleanup if an interrupt signal is caught
func (c *Parser) CleanupOnInterrupt() {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt)
	go func() {
		for range ch {
			c.cleanup()
			os.Exit(1)
		}
	}()
}

func sliceContains(sl []string, target string) bool {
	for _, s := range sl {
		if s == target {
			return true
		}
	}
	return false
}
