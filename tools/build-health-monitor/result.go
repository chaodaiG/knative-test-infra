/*
build-health is a tool downloading test results from Testgrid,
analyze all runs from all repos, and reports if there is any job has
recent failures, or if start time of latest run being very old, i.e.
3 times earlier than median interval
*/

package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sort"
	"time"
)

var (
	baseURL   = "https://testgrid.knative.dev/"
	cMax      = 10 // max count of runs to be reported
	repoNames = []string{
		"serving",
		"build",
		"build-pipeline",
		"eventing",
		"eventing-sources",
		"docs",
		"build-templates",
		"pkg",
	}

	client = &http.Client{Timeout: 10 * time.Second}
)

// Job is for json unmarshal
type Job struct {
	Tests      []Test  `json:"tests"`
	Timestamps []int64 `json:"timestamps"`
}

// Test is for json unmarshal
type Test struct {
	Name     string   `json:"name"`
	Statuses []Status `json:"statuses`
}

// Status is for json unmarshal
type Status struct {
	Count int `json:"count"`
	Value int `json:"value"`
}

// help method for http GET then unmarshal it
func getJSON(url string, target interface{}) error {
	r, err := client.Get(url)
	if nil != err {
		return err
	}
	defer r.Body.Close()
	tmp := json.NewDecoder(r.Body)
	return tmp.Decode(target)
}

// check whether job all passed in last cMax runs,
// whether last completed run started not too long ago
func isGood(job *Job, cMax int) bool {
	// get median intervals
	tms := job.Timestamps
	if len(tms) == 0 {
		return true
	}
	if len(tms) > cMax {
		tms = tms[:cMax]
	}
	itvs := make([]int64, len(tms)-1, len(tms)-1)
	for i := 1; i < len(tms); i++ {
		itvs[i-1] = tms[i] - tms[i-1]
	}
	if len(itvs) == 0 {
		return true
	}
	sort.Slice(itvs, func(i, j int) bool {
		return itvs[i] > itvs[j]
	})
	mItv := itvs[len(itvs)/2]

	allowedItv := mItv * 3 // Warn latest run finished earlier than 3 times of normal intervals
	if time.Now().Unix()-tms[0] > int64(allowedItv) {
		log.Printf("got interval %d, want interval %d", time.Now().Unix(), tms[0] > int64(allowedItv))
		return false
	}

	for _, test := range job.Tests {
		if test.Name != "Overall" {
			continue
		}
		for _, status := range test.Statuses {
			for i := 0; cMax > 0 && i < status.Count; i++ {
				cMax--
				if status.Value != 1 {
					return false
				}
			}
		}
	}
	return true
}

func printLatestRuns(job *Job, cMax int) {
	for _, test := range job.Tests {
		if "Overall" == test.Name {
			res := ""
			for _, status := range test.Statuses {
				for i := 0; cMax > 0 && i < status.Count; i++ {
					cMax--
					switch status.Value {
					case 1:
						res += fmt.Sprintf("%-5s", "P")
					case 12:
						res += fmt.Sprintf("%-5s", "X")
					default:
						res += fmt.Sprintf("%-5s", "Other")
					}
				}
			}
			log.Println(res)
			break
		}
	}
}

func printTestCount(job *Job, cMax int) {
	res := make([][]int, cMax)
	for _, test := range job.Tests {
		c := 0
		for _, status := range test.Statuses {
			for i := 0; c < cMax && i < status.Count; i++ {
				if nil == res[c] || len(res[c]) == 0 {
					res[c] = make([]int, 2)
				}
				switch status.Value {
				case 0, 1:

				default:
					res[c][0]++
				}
				if 0 != status.Value {
					res[c][1]++
				}
				c++
			}
		}
	}

	o := make([]string, 2, 2)
	for _, val := range res {
		if len(val) == 0 {
			continue
		}
		for i := 0; i < 2; i++ {
			o[i] += fmt.Sprintf("%-5d", val[i])
		}
	}
	for i := 0; i < 2; i++ {
		log.Println(o[i])
	}
}

// getTabs get json of repo summary, unmarshal it and return jobs names
// r: repoName
func getTabs(r string) (res []string, err error) {
	s := fmt.Sprintf("%sknative-%s/summary", baseURL, r)
	t := make(map[string]interface{})
	if err = getJSON(s, &t); nil != err {
		return
	}

	for key := range t {
		if "" != key {
			res = append(res, key)
		}
	}
	return
}

// getJob get json of one job tab and unmarshal it
// r: repoName, j: jobName
func getJob(r, j string) (job *Job, err error) {
	s := fmt.Sprintf("%sknative-%s/table?tab=%s", baseURL, r, j)
	job = &Job{}
	err = getJSON(s, job)
	return
}

func main() {
	for _, r := range repoNames {
		tabs, err := getTabs(r)
		if nil != err {
			log.Println("Failed getting tabs: ", err)
			continue
		}
		for _, tab := range tabs {
			if "playground" == tab || "" == tab {
				continue
			}
			job, err := getJob(r, tab)
			if nil != err {
				log.Println("Failed getting data from job: ", err)
				continue
			}
			if isGood(job, cMax) {
				continue
			}
			log.Println(r, tab)
			printLatestRuns(job, cMax)
			printTestCount(job, cMax)
		}
	}
}
