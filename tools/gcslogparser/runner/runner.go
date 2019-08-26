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
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"regexp"
	"strings"

	"knative.dev/test-infra/shared/gcs"
)

const (
	serviceAccount = "SERVICE_ACCOUNT"
)

// Matching gs://BUCKETNAME/FILEPATH or BUCKETNAME/FILEPATH
// Lazy matching
var (
	ctx = context.Background()
	re  = regexp.MustCompile(`(gs://)?(.*?)/(.*)`)
)

func getBucketAndFilepath(gcsPath string) (string, string, error) {
	parts := re.FindStringSubmatch(gcsPath)
	if len(parts) == 0 {
		return "", "", fmt.Errorf("Path '%s' has to be one of: 'gs://BUCKETNAME/FILEPATH' or 'BUCKETNAME/FILEPATH'", gcsPath)
	}
	iStart := 1
	if len(parts) == 4 { // contains gs://
		iStart++
	}
	return parts[iStart], parts[iStart+1], nil
}

func readGcsFile(gcsPath string) ([]byte, error) {
	log.Println("Read file: ", gcsPath)
	bucketName, filepath, _ := getBucketAndFilepath(gcsPath)
	return gcs.Read(ctx, bucketName, filepath)
}

type payload struct {
	Path         string `json:"path"`
	Query        string `json:"query"`
	QueryPattern string `json:"query_pattern"` // regex
}

func handler(w http.ResponseWriter, r *http.Request) {
	p := payload{}
	log.Print("Received a request.")
	body, err := ioutil.ReadAll(io.LimitReader(r.Body, 1048576))
	if err != nil {
		panic(err)
	}
	if err := r.Body.Close(); err != nil {
		panic(err)
	}

	log.Printf("Unmarshal request body: '%s'", string(body))
	w.Header().Set("Content-Type", "application/json; charset=UTF-8")
	if err := json.Unmarshal(body, &p); err != nil {
		log.Printf("failed unmarshal request body: '%s'", string(body))
		w.WriteHeader(422) // unprocessable entity
		if err := json.NewEncoder(w).Encode(err); err != nil {
			panic(err)
		}
		return
	}

	log.Printf("Reading gcs file: '%s'", p.Path)
	contents, err := readGcsFile(p.Path)
	if nil != err {
		log.Printf("failed read gcs file: '%s'", p.Path)
		w.WriteHeader(http.StatusNotFound)
		if err := json.NewEncoder(w).Encode(err); err != nil {
			panic(err)
		}
		return
	}

	log.Printf("Checking whether contains string: '%s'", p.Query)
	returnStatus := http.StatusOK
	found := "true"
	if "" != p.Query {
		if !strings.Contains(string(contents), p.Query) {
			found = "false"
		}
	}
	if "" != p.QueryPattern {
		pattern, err := regexp.Compile(p.QueryPattern)
		if nil != err {
			returnStatus = http.StatusBadRequest
			found = "false"
		} else {
			if !pattern.MatchString(string(contents)) {
				found = "false"
			}
		}
	}
	w.WriteHeader(returnStatus)
	if err := json.NewEncoder(w).Encode(p.Path + ";" + found); err != nil {
		log.Printf("failed writes to response writer")
		panic(err)
	}
}

func main() {
	log.Print("Service started.")

	gcs.Authenticate(ctx, os.Getenv(serviceAccount))

	http.HandleFunc("/", handler)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	log.Fatal(http.ListenAndServe(fmt.Sprintf(":%s", port), nil))
}
