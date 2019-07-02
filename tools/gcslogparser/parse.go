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
	"log"
	"os"
	"regexp"
	"sort"
	"strings"
)

type Flags struct {
	serviceAccount string
	repoNames      string
	startDate      string
	parseRegex     string
	jobFilter      string
	prOnly         bool
	ciOnly         bool
	groupBy        string
}

func parseOptions() *Flags {
	var f Flags
	flag.StringVar(&f.serviceAccount, "service-account", os.Getenv("GOOGLE_APPLICATION_CREDENTIALS"), "JSON key file for GCS service account")
	flag.StringVar(&f.repoNames, "repo", "test-infra", "repo to be analyzed, comma separated")
	flag.StringVar(&f.startDate, "start-date", "2017-01-01", "cut off date to be analyzed")
	flag.StringVar(&f.parseRegex, "parser", "", "regex string used for parsing")
	flag.StringVar(&f.jobFilter, "jobs", "", "jobs to be analyzed, comma separated")
	flag.BoolVar(&f.prOnly, "pr-only", false, "supplied if just want to analyze PR jobs")
	flag.BoolVar(&f.ciOnly, "ci-only", false, "supplied if just want to analyze CI jobs")
	flag.StringVar(&f.groupBy, "groupby", "job(default)", "output groupby, supports: match(group by matches)")
	flag.Parse()
	return &f
}

// func getPreviousDay(dateStr string) string {
// 	today, _ := time.Parse(time.RFC3339, dateStr+"T00:00:00.000Z")
// 	year, month, date := today.Add(-24 * time.Hour)
// 	return year + "-" + month + "-" + date
// }

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
	c, _ := NewParser(f.serviceAccount)
	c.logParser = func(s string) string {
		return regexp.MustCompile(f.parseRegex).FindString(s)
	}
	c.CleanupOnInterrupt()
	defer c.cleanup()

	c.setStartDate(f.startDate)
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
	// realJobFilter := f.jobFilter
	// realRepoNames := f.repoNames
	// realDate := f.startDate
	// f.jobFilter = "ci-knative-serving-nightly-release"
	// f.repoNames = "serving"
	// f.startDate = getPreviousDay(realDate)
	// fc := parse(f)
	// sort.Slice(fc, func(i, j int) bool {
	// 	return path.Base(fc[i]) < path.Base(fc[j])
	// })

	// f.jobFilter = realJobFilter
	// f.repoNames = realRepoNames
	c := parse(f)
	log.Printf("Processed %d builds, and found %d matches", len(c.processed), len(c.found))
	switch f.groupBy {
	case "job":
		groupByJob(c.found)
	case "match":
		groupByMatch(c.found)
	default:
		log.Printf("--groupby doesn't support %s, fallback to default", f.groupBy)
		groupByJob(c.found)
	}
}
