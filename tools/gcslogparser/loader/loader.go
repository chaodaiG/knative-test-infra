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
	"os/exec"
	"sync"
)

const (
	cycles = 1
)

func curlPost(host, ip, payload string) ([]byte, error) {
	cmds := []string{
		"-H",
		"Host:" + host,
		"-d",
		payload,
		ip,
	}
	cmd := exec.Command("curl", cmds...)
	return cmd.Output()
}

func restPost(host, ip string, payload interface{}) ([]byte, error) {
	jsonValue, _ := json.Marshal(payload)
	request, _ := http.NewRequest("POST", "http://"+ip, bytes.NewBuffer(jsonValue))
	request.Header.Set("Content-Type", "application/json")
	client := &http.Client{}
	request.Host = host
	response, err := client.Do(request)
	log.Println(response.StatusCode)
	if err != nil {
		return []byte(""), fmt.Errorf("the HTTP request failed with error %s\n", err)
	} else {
		data, err := ioutil.ReadAll(response.Body)
		return data, err
	}
}

func main() {
	res := make([]string, cycles)
	wg := sync.WaitGroup{}
	for i := 0; i < cycles; i++ {
		i := i
		wg.Add(1)
		go func(wg *sync.WaitGroup) {
			// output, err := curlPost(
			// 	"Host:gcslogparser-runner-image.default.example.com",
			// 	"35.239.246.232",
			// 	"{\"path\":\"gs://knative-prow/pr-logs/pull/knative_serving/4876/pull-knative-serving-build-tests/1154470644423856128/build-log.txt\",\"query\":\"go\"}",
			// )

			payload := map[string]string{
				"path":  "gs://knative-prow/pr-logs/pull/knative_serving/4876/pull-knative-serving-build-tests/1154470644423856128/build-log.txt",
				"query": "go",
			}

			output, err := restPost(
				"gcslogparser-runner-image.default.example.com",
				"35.239.246.232",
				payload,
			)
			if err != nil {
				log.Println(err)
				res[i] = "gs://knative-prow/pr-logs/pull/knative_serving/4876/pull-knative-serving-build-tests/1154470644423856128/build-log.txt"
			} else {
				res[i] = string(output)
			}
			if i%100 == 0 {
				log.Println("done ", i)
			}
			wg.Done()
		}(&wg)
	}
	wg.Wait()
	log.Println(res)
}
