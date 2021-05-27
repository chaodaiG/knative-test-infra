package main

import (
	"context"
	"flag"
	"log"
	"path"
	"sort"
	"sync"
	"time"

	"knative.dev/test-infra/pkg/gcs"
	"knative.dev/test-infra/pkg/prow"
	"knative.dev/test-infra/tools/last-succeed-build/cache"
)

const (
	defaultServiceAccountKey = ""
)

type options struct {
	serviceAccount string
	bucket         string

	project string
	dataset string
	table   string
}

func parseOptions() options {
	var o options
	flag.StringVar(&o.serviceAccount, "service-account", "", "")
	flag.StringVar(&o.bucket, "bucket", "", "")
	flag.StringVar(&o.project, "project", "", "")
	flag.StringVar(&o.dataset, "dataset", "", "")
	flag.StringVar(&o.table, "table", "", "")
	flag.Parse()
	return o
}

func processJobChan(ctx context.Context, cacheClient *cache.Client, jobChan chan prow.Job, wg *sync.WaitGroup) {
	for {
		select {
		case job := <-jobChan:
			reportLatestValidBuild(cacheClient, &job)
			wg.Done()
		case <-ctx.Done():
			return
		}
	}
}

// getLatestValidBuild inexpensively sorts and finds the most recent JSON report.
// Assumes sequential build IDs are sequential in time.
func reportLatestValidBuild(cachClient *cache.Client, job *prow.Job) {
	// look at older builds
	maxElapsedTime := time.Hour * 24 * 90
	buildIDs := job.GetBuildIDs()
	sort.Sort(sort.Reverse(sort.IntSlice(buildIDs)))
	var first bool
	for _, buildID := range buildIDs {
		if cachClient.Updated(context.Background(), int64(buildID)) {
			continue
		}
		build := job.NewBuild(buildID)
		// check if this report is too old
		finishedJSON, err := build.GetFinishedJSON()
		if err != nil {
			log.Printf("Debug: finished.json not found for build %s. %v", build.StoragePath, err)
			continue
		}

		startTime := time.Unix(finishedJSON.Timestamp, 0)
		if !first {
			log.Printf("Last finished build for %s at %v", build.StoragePath, startTime)
		}
		first = true
		if time.Since(startTime) > maxElapsedTime {
			return
		}

		if finishedJSON.Passed {
			log.Printf("PASSED! %s at %v", build.StoragePath, startTime)
		}

		// Don't care that much it failed
		if err := cachClient.Insert(context.Background(), []*cache.BuildInfo{
			&cache.BuildInfo{
				BuildID:   int64(buildID),
				JobName:   build.JobName,
				Updated:   true,
				Timestamp: finishedJSON.Timestamp,
				Passed:    finishedJSON.Passed,
			},
		}); err != nil {
			log.Print(err)
		}
	}
}

func processJobs(jobGcsPrefixes []string, jobChan chan prow.Job, wg *sync.WaitGroup) {
	for _, jobGcsPrefix := range jobGcsPrefixes {
		if jobGcsPrefix == "logs" {
			continue
		}
		log.Println(path.Base(jobGcsPrefix))
		job := prow.NewJob(path.Base(jobGcsPrefix), "periodic", "", "", 0)
		latest, err := job.GetLatestBuildNumber()
		if err != nil {
			log.Printf("Warning: latest build id not found for %s", jobGcsPrefix)
		}
		log.Printf("Latest build for %s is %d", jobGcsPrefix, latest)
		wg.Add(1)
		jobChan <- *job
	}
}

func main() {
	o := parseOptions()
	gcsClient, err := gcs.NewClient(context.Background(), o.serviceAccount)
	if err != nil {
		log.Fatal(err)
	}
	prow.Initialize(o.serviceAccount)

	cacheClient, err := cache.NewClient(context.Background(), o.project, o.dataset, o.table)
	if err != nil {
		log.Fatal(err)
	}
	defer cacheClient.Close()
	if err := cacheClient.Load(context.Background()); err != nil {
		log.Fatal(err)
	}
	jobs, err := gcsClient.ListDirectChildren(context.Background(), o.bucket, "logs/")
	if err != nil {
		log.Fatal(err)
	}
	var wg sync.WaitGroup
	jobChan := make(chan prow.Job, 200)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	for i := 0; i < 20; i++ {
		go processJobChan(ctx, cacheClient, jobChan, &wg)
	}
	processJobs(jobs, jobChan, &wg)
	wg.Wait()

	log.Println("All Done")
}
