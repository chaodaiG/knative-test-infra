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
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"time"

	"knative.dev/test-infra/shared/prow"
)

// finding is the basic element to be returned for all builds that match with
// query
type finding struct {
	// match is the string that matches with query
	match     string
	timestamp string
	gcsPath   string
}

type Parser struct {
	cacheHandler *CacheHandler
	ParseRegex   string
	Parse        string
	StartDate    time.Time             // Earliest date to be analyzed, i.e. "2019-02-22"
	EndDate      time.Time             // Latest date to be analyzed, i.e. "2019-02-22"
	logParser    func(s string) string // logParser function
	jobFilter    []string              // Jobs to be parsed. If not provided will parse all jobs
	PrChan       chan prInfo           // For PR use only, make it here so it's easier to cleanup
	jobChan      chan prow.Job
	buildChan    chan buildInfo
	wgPR         sync.WaitGroup
	wgJob        sync.WaitGroup
	wgBuild      sync.WaitGroup

	found     []finding
	processed []string

	runnerHost string
	runnerIP   string

	buildIDChan chan int // keeps the highest buildID which started before StartDate

	mutex        *sync.Mutex
	buildIDMutex *sync.Mutex

	start time.Time

	failedCount      int
	failedCountMutex *sync.Mutex
}

type buildInfo struct {
	job prow.Job
	ID  int
}

// Check to see if the buildID is older than currently found old buildID
func (c *Parser) buildIDTooOld(buildID int) bool {
	c.buildIDMutex.Lock()
	defer c.buildIDMutex.Unlock()
	tooOld := <-c.buildIDChan
	res := (buildID < tooOld)
	go func() { c.buildIDChan <- tooOld }()
	// log.Printf("build ID %d %v too old", buildID, res)
	return res
}

func (c *Parser) updateBuildIDChan(newVal int, comp func(int) bool) bool {
	var res bool
	c.buildIDMutex.Lock()
	defer c.buildIDMutex.Unlock()
	tooOld := <-c.buildIDChan
	if comp(tooOld) {
		tooOld = newVal
		res = true
	}
	go func() { c.buildIDChan <- tooOld }()
	return res
}

func NewParser(serviceAccount string) (*Parser, error) {
	var err error
	if err = prow.Initialize(serviceAccount); nil != err { // Explicit authenticate with gcs Parser
		return nil, fmt.Errorf("Failed authenticating GCS: '%v'", err)
	}

	c := &Parser{}
	c.cacheHandler, err = NewCacheHandler(nil)
	if nil != err {
		return nil, err
	}
	c.start = time.Now()
	c.failedCount = 0
	c.mutex = &sync.Mutex{}
	c.buildIDMutex = &sync.Mutex{}
	c.failedCountMutex = &sync.Mutex{}

	c.PrChan = make(chan prInfo, 1000)
	c.jobChan = make(chan prow.Job, 5000)
	c.buildChan = make(chan buildInfo, 10000)

	c.buildIDChan = make(chan int, 1)
	c.buildIDChan <- -1

	for i := 0; i < 200; i++ {
		go c.jobListener()
	}
	for i := 0; i < 200; i++ {
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

func (c *Parser) setEndDate(endDate string) error {
	if "" == endDate {
		c.EndDate = time.Now().Add(time.Hour * 144)
		return nil
	}
	tt, err := time.Parse("2006-01-02", endDate)
	if nil != err {
		return fmt.Errorf("invalid start date string, expecing format YYYY-MM-DD: '%v'", err)
	}
	c.EndDate = tt
	return nil

}

func (c *Parser) feedSingleBuild(j prow.Job, buildID int) {
	// if isTooOld := c.buildIDTooOld(buildID); isTooOld {
	// 	// log.Printf("skipping %d %s", buildID, j.StoragePath)
	// 	return
	// }
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
			// Sleep 2 seconds so baseline is established
			time.Sleep(2 * time.Second)
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
			cached, err := c.cacheHandler.GetBuildInfo(&b.job, b.ID)
			if nil != err {
				c.wgBuild.Done()
				log.Fatalf("failed reading from cache handler: '%v'", err)
			}
			if nil != cached && cached.StartTime > c.StartDate.Unix() && cached.EndTime < c.EndDate.Unix() {
				payload := map[string]string{
					"path":          "gs://knative-prow/" + cached.GcsPath,
					"query_pattern": c.ParseRegex,
					"query":         c.Parse,
				}
				jsonValue, _ := json.Marshal(payload)
				err := fmt.Errorf("foobar error")
				for retry := 3; retry > 0 && nil != err; retry-- {
					request, _ := http.NewRequest("POST", "http://"+c.runnerIP, bytes.NewBuffer(jsonValue))
					request.Header.Set("Content-Type", "application/json")
					request.Header.Set("Host", "gcslogparser-runner-image.default.example.com")
					request.Host = c.runnerHost
					client := &http.Client{}

					var response *http.Response
					response, err = client.Do(request)
					if nil != err {
						errStr := ""
						if nil != response {
							errStr += response.Status
						}
						err = fmt.Errorf("failed client.Do: '%v' '%s'", err, errStr)
						continue
					}
					if response == nil {
						err = fmt.Errorf("response is nil")
						continue
					}
					defer response.Body.Close()
					if response.StatusCode != http.StatusOK {
						err = fmt.Errorf("response code is: %v", response.StatusCode)
						continue
					}
					var output []byte
					output, err = ioutil.ReadAll(response.Body)
					if nil != err {
						err = fmt.Errorf("failed read response body: '%v'", err)
						continue
					}

					match := ""
					// Result will be returned in the form of
					// [GCS_PATH];[MATCH_STRING], split on first occurance of ";"
					parts := strings.SplitN(strings.Trim(string(output), "\n\r\"'"), ";", 2)
					if len(parts) > 1 { // single element in parts means it failed
						match = strings.Trim(parts[1], " ")
						c.mutex.Lock()
						c.processed = append(c.processed, cached.GcsPath)
						if match != "" {
							c.found = append(c.found, finding{match, time.Unix(cached.StartTime, 0).String(), cached.GcsPath})
						}
						c.mutex.Unlock()
						err = nil
					} else {
						err = fmt.Errorf("error in runner service itself, only returned single part")
						time.Sleep(time.Second * 3) // Wait for 3 seconds before retrying
					}
				}
				if nil != err {
					c.failedCountMutex.Lock()
					c.failedCount++
					c.failedCountMutex.Unlock()
					log.Printf("Warning: Failed parsing '%s' with err: '%v'", cached.GcsPath, err)
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
