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
	"flag"
	"fmt"
	"log"
	"os"
	"sort"
	"strings"
)

type Flags struct {
	serviceAccount string
	repoNames      string
	startDate      string
	endDate        string
	parseRegex     string
	jobFilter      string
	prOnly         bool
	ciOnly         bool
	groupBy        string
	runnerHost     string
	runnerIP       string
}

func parseOptions() *Flags {
	var f Flags
	flag.StringVar(&f.serviceAccount, "service-account", os.Getenv("GOOGLE_APPLICATION_CREDENTIALS"), "JSON key file for GCS service account")
	flag.StringVar(&f.repoNames, "repo", "test-infra", "repo to be analyzed, comma separated")
	flag.StringVar(&f.startDate, "start-date", "2017-01-01", "cut off date to be analyzed")
	flag.StringVar(&f.endDate, "end-date", "", "cut off date to be analyzed")
	flag.StringVar(&f.parseRegex, "parser", "", "regex string used for parsing")
	flag.StringVar(&f.jobFilter, "jobs", "", "jobs to be analyzed, comma separated")
	flag.BoolVar(&f.prOnly, "pr-only", false, "supplied if just want to analyze PR jobs")
	flag.BoolVar(&f.ciOnly, "ci-only", false, "supplied if just want to analyze CI jobs")
	flag.StringVar(&f.groupBy, "groupby", "job(default)", "output groupby, supports: match(group by matches)")
	flag.StringVar(&f.runnerHost, "host", "gcslogparser-runner-image.default.example.com", "host name of runner service)")
	flag.StringVar(&f.runnerIP, "ip", "34.68.162.48", "ip address of runner service")
	flag.Parse()
	return &f
}

func groupByJob(found [][]string) {
	var msgs []string
	for _, elems := range found {
		msgs = append(msgs, strings.Join(elems, ","))
	}
	log.Printf("\n\n%s", strings.Join(msgs, "\n"))
}

func groupByMatch(found [][]string) {
	outArr := make(map[string][][]string)
	for _, l := range found {
		if _, ok := outArr[l[0]]; !ok {
			outArr[l[0]] = make([][]string, 0, 0)
		}
		outArr[l[0]] = append(outArr[l[0]], l)
	}
	for _, sl := range outArr {
		sort.Slice(sl, func(i, j int) bool {
			return sl[i][1] > sl[j][1]
		})
		var msgs []string
		for _, elems := range sl {
			msgs = append(msgs, strings.Join(elems, ","))
		}
		log.Printf("\n\n%s", strings.Join(msgs, "\n"))
	}
}

func parse(f *Flags) *Parser {
	c, err := NewParser(f.serviceAccount)
	if nil != err {
		log.Fatal(err)
	}
	// c.logParser = func(s string) string {
	// 	return regexp.MustCompile(f.parseRegex).FindString(s)
	// }
	c.ParseString = f.parseRegex
	c.runnerHost = f.runnerHost
	c.runnerIP = f.runnerIP
	c.CleanupOnInterrupt()
	defer c.cleanup()
	defer c.cacheHandler.Save()

	c.setStartDate(f.startDate)
	c.setEndDate(f.endDate)
	for _, j := range strings.Split(f.jobFilter, ",") {
		if "" != j {
			c.jobFilter = append(c.jobFilter, j)
		}
	}

	for _, repo := range strings.Split(f.repoNames, ",") {
		log.Printf("Repo: '%s'", repo)
		if !f.prOnly {
			log.Println("\tProcessing postsubmit jobs")
			c.feedPostsubmitJobsFromRepo(repo)
		}
		if !f.ciOnly {
			log.Println("\tProcessing presubmit jobs")
			c.feedPresubmitJobsFromRepo(repo)
		}
	}
	c.wait()
	return c
}

func main() {
	f := parseOptions()
	if len(f.parseRegex) == 0 {
		log.Fatal("--parser must be provided")
	}

	c := parse(f)
	switch f.groupBy {
	case "job":
		groupByJob(c.found)
	case "match":
		groupByMatch(c.found)
	default:
		log.Printf("--groupby doesn't support %s, fallback to default", f.groupBy)
		groupByJob(c.found)
	}
	summary := fmt.Sprintf("Summary:\nQuerying jobs from repos: '%s'", f.repoNames)
	summary = fmt.Sprintf("%s\nQuerying pattern: '%s'", summary, f.parseRegex)
	if "" != f.startDate {
		summary = fmt.Sprintf("%s\nStart date: %s", summary, f.startDate)
	}
	if "" != f.endDate {
		summary = fmt.Sprintf("%s\nEnd date: %s", summary, f.endDate)
	}
	summary = fmt.Sprintf("%s\nResults:\n\tProcessed jobs: %d\n\tFound matches: %d\n\n\n\n\nNo log found: %d",
		summary, len(c.processed), len(c.found), c.failedCount)

	log.Print(summary)

}
