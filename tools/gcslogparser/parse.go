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
	"regexp"
	"sort"
	"strings"
)

var (
	globalFlag Flags
)

type Flags struct {
	serviceAccount string
	repoNames      string
	startDate      string
	endDate        string
	parseRegex     string
	parse          string
	jobFilter      string
	prOnly         bool
	ciOnly         bool
	groupBy        string
	runnerHost     string
	runnerIP       string
}

func parseOptions() {
	globalFlag = Flags{}
	flag.StringVar(&globalFlag.serviceAccount, "service-account", os.Getenv("GOOGLE_APPLICATION_CREDENTIALS"), "JSON key file for GCS service account")
	flag.StringVar(&globalFlag.repoNames, "repo", "test-infra", "repo to be analyzed, comma separated")
	flag.StringVar(&globalFlag.startDate, "start-date", "2017-01-01", "cut off date to be analyzed")
	flag.StringVar(&globalFlag.endDate, "end-date", "", "cut off date to be analyzed")
	flag.StringVar(&globalFlag.parseRegex, "parser-regex", "", "regex string used for parsing")
	flag.StringVar(&globalFlag.parse, "parser", "", "string used for parsing")
	flag.StringVar(&globalFlag.jobFilter, "jobs", "", "jobs to be analyzed, comma separated")
	flag.BoolVar(&globalFlag.prOnly, "pr-only", false, "supplied if just want to analyze PR jobs")
	flag.BoolVar(&globalFlag.ciOnly, "ci-only", false, "supplied if just want to analyze CI jobs")
	flag.StringVar(&globalFlag.groupBy, "groupby", "job(default)", "output groupby, supports: match(group by matches), repo(group by repo")
	flag.StringVar(&globalFlag.runnerHost, "host", "gcslogparser-runner-image.default.example.com", "host name of runner service)")
	flag.StringVar(&globalFlag.runnerIP, "ip", "34.68.162.48", "ip address of runner service")
	flag.Parse()
	if _, err := regexp.Compile(globalFlag.parseRegex); err != nil {
		log.Fatalf("bad matching regex: %q", globalFlag.parseRegex)
	}
}

func groupByJob(found []finding) string {
	sort.Slice(found, func(i, j int) bool {
		return found[i].timestamp < found[j].timestamp
	})
	return printMsg(found)
}

func groupByMatch(found []finding) string {
	msg := ""
	outArr := make(map[string][]finding)
	keys := make([]string, 0)
	for _, l := range found {
		if _, ok := outArr[l.match]; !ok {
			outArr[l.match] = make([]finding, 0)
			keys = append(keys, l.match)
		}
		outArr[l.match] = append(outArr[l.match], l)
	}

	sort.Strings(keys)
	for _, key := range keys {
		sl := outArr[key]
		sort.Slice(sl, func(i, j int) bool {
			return sl[i].timestamp > sl[j].timestamp
		})
		msg += printMsg(sl)
	}
	return msg
}

func groupByRepo(found []finding) string {
	sort.Slice(found, func(i, j int) bool {
		if getJobName(found[i].gcsPath) != getJobName(found[j].gcsPath) {
			return getJobName(found[i].gcsPath) < getJobName(found[j].gcsPath)
		}
		return found[i].timestamp < found[j].timestamp
	})
	return printMsg(found)
}

func printMsg(found []finding) string {
	var releaseJobs []finding
	var presubmitJobs []finding
	var nightlyJobs []finding
	var otherJobs []finding
	for _, elems := range found {
		jobName := getJobName(elems.gcsPath)
		if strings.Contains(jobName, "release") {
			releaseJobs = append(releaseJobs, elems)
		} else if strings.HasPrefix(jobName, "pull") {
			presubmitJobs = append(presubmitJobs, elems)
		} else if strings.HasPrefix(jobName, "ci") {
			nightlyJobs = append(nightlyJobs, elems)
		} else {
			otherJobs = append(otherJobs, elems)
		}
	}

	msg := fmt.Sprintf("\n\t\tRelease jobs: %d\n\t\tPresubmit jobs: %d\n\t\tNightly jobs: %d\n\t\tOther jobs: %d",
		len(releaseJobs), len(presubmitJobs), len(nightlyJobs), len(otherJobs))

	return fmt.Sprintf("%s\n\n%s\n%s\n%s", msg, msgForType(releaseJobs, "Release Jobs"), msgForType(presubmitJobs, "Presubmit jobs"),
		msgForType(otherJobs, "Other Jobs"))
}

func msgForType(found []finding, name string) string {
	msg := fmt.Sprintf("%s(%d):", name, len(found))
	for _, elems := range found {
		match := elems.match
		if globalFlag.parseRegex != "" {
			if tmp := regexp.MustCompile(globalFlag.parseRegex).FindStringSubmatch(match); len(tmp) >= 2 {
				match = strings.Join(tmp[1:], "|||")
			}
		}
		msg = fmt.Sprintf("%s\n\t%s, %s, %s", msg, match, elems.timestamp, elems.gcsPath)
	}
	return msg
}

func getJobName(jobURL string) string {
	for _, part := range strings.Split(jobURL, "/") {
		if strings.Contains(part, "-knative-") {
			return part
		}
	}
	return ""
}

func parse(f *Flags) *Parser {
	c, err := NewParser(globalFlag.serviceAccount)
	if nil != err {
		log.Fatal(err)
	}
	// c.logParser = func(s string) string {
	// 	return regexp.MustCompile(globalFlag.parseRegex).FindString(s)
	// }
	c.ParseRegex = globalFlag.parseRegex
	c.Parse = globalFlag.parse
	c.runnerHost = globalFlag.runnerHost
	c.runnerIP = globalFlag.runnerIP
	c.CleanupOnInterrupt()
	defer c.cleanup()
	defer c.cacheHandler.Save()

	c.setStartDate(globalFlag.startDate)
	c.setEndDate(globalFlag.endDate)
	for _, j := range strings.Split(globalFlag.jobFilter, ",") {
		if "" != j {
			c.jobFilter = append(c.jobFilter, j)
		}
	}

	for _, repo := range strings.Split(globalFlag.repoNames, ",") {
		log.Printf("Repo: '%s'", repo)
		if !globalFlag.prOnly {
			log.Println("\tProcessing postsubmit jobs")
			c.feedPostsubmitJobsFromRepo(repo)
		}
		if !globalFlag.ciOnly {
			log.Println("\tProcessing presubmit jobs")
			c.feedPresubmitJobsFromRepo(repo)
		}
	}
	c.wait()
	return c
}

func main() {
	parseOptions()
	if len(globalFlag.parseRegex) == 0 && len(globalFlag.parse) == 0 {
		log.Fatal("--parser or --parser-regex must be provided")
	}

	c := parse(&globalFlag)

	summary := fmt.Sprintf("Summary:\nQuerying jobs from repos: '%s'", globalFlag.repoNames)
	summary = fmt.Sprintf("%s\nQuerying pattern: '%s'", summary, globalFlag.parse+globalFlag.parseRegex)
	if "" != globalFlag.startDate {
		summary = fmt.Sprintf("%s\nStart date: %s", summary, globalFlag.startDate)
	}
	if "" != globalFlag.endDate {
		summary = fmt.Sprintf("%s\nEnd date: %s", summary, globalFlag.endDate)
	}
	summary = fmt.Sprintf("%s\nResults:\n\tProcessed jobs: %d\n\tFound matches: %d",
		summary, len(c.processed), len(c.found))

	var ind string
	switch globalFlag.groupBy {
	case "job":
		ind = groupByJob(c.found)
	case "match":
		ind = groupByMatch(c.found)
	case "repo":
		ind = groupByRepo(c.found)
	default:
		// log.Printf("--groupby doesn't support %s, fallback to default", globalFlag.groupBy)
		ind = groupByJob(c.found)
	}

	summary = fmt.Sprintf("%s%s", summary, ind)
	log.Println(summary)

	log.Printf("\n\n\n\n\nNo log found: %d", c.failedCount)
}
