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
	"io/ioutil"
	"log"
	"path"
	"regexp"
	"sync"

	"knative.dev/test-infra/shared/gcs"

	"knative.dev/test-infra/shared/prow"
)

var (
	defaultCachePath = "gs://gcslogparser-cache/knative-jobs"
	ctx              = context.Background()
	re               = regexp.MustCompile(`(gs://)?(.*?)/(.*)`)
)

func getBucketAndFilepath(gcsPath string) (string, string, error) {
	parts := re.FindStringSubmatch(gcsPath)
	if len(parts) < 3 {
		return "", "", fmt.Errorf("Path '%s' has to be one of: 'gs://BUCKETNAME/FILEPATH' or 'BUCKETNAME/FILEPATH'", gcsPath)
	}
	iStart := 1
	if len(parts) >= 4 { // contains gs://
		iStart++
	}
	return parts[iStart], parts[iStart+1], nil
}

type CacheHandler struct {
	buildInfos map[string]BuildInfo
	cachePath  string

	mutex sync.Mutex
}

type BuildInfos struct {
	All []BuildInfo
}

type BuildInfo struct {
	GcsPath   string `json:"gcsPath"`
	StartTime int64  `json:"startTime"`
	Finished  bool   `json:"finished"`
}

func NewCacheHandler(cachePath *string) (*CacheHandler, error) {
	c := CacheHandler{}
	if nil == cachePath {
		cachePath = &defaultCachePath
	}
	c.cachePath = *cachePath
	err := c.Load()
	return &c, err
}

func (c *CacheHandler) Load() error {
	c.buildInfos = make(map[string]BuildInfo)
	bucketName, filePath, err := getBucketAndFilepath(c.cachePath)
	if nil != err {
		return fmt.Errorf("cachePath must be a valid gcs path: '%s'", c.cachePath)
	}
	if !gcs.Exists(ctx, bucketName, filePath) {
		log.Printf("INFO: cache path not exist yet, will start a new one: '%s'", c.cachePath)
		return nil
	}
	contents, err := gcs.Read(ctx, bucketName, filePath)
	if nil != err {
		return err
	}
	buildInfos := BuildInfos{}
	if err = json.Unmarshal(contents, &buildInfos.All); nil != err {
		return err
	}
	for _, buildInfo := range buildInfos.All {
		c.buildInfos[buildInfo.GcsPath] = buildInfo
	}
	return nil
}

func (c *CacheHandler) GetBuildInfo(job *prow.Job, ID int) (*BuildInfo, error) {
	buildGcsPath := fmt.Sprintf("%s/%d/build-log.txt", job.StoragePath, ID)
	var buildInfo BuildInfo
	var ok bool
	c.mutex.Lock()
	buildInfo, ok = c.buildInfos[buildGcsPath]
	c.mutex.Unlock()
	if !ok || buildInfo.StartTime == 0 || buildInfo.Finished == false {
		build := job.NewBuild(ID)
		if !build.IsStarted() {
			return nil, nil
		}
		startTime, err := build.GetStartTime()
		if nil != err {
			return nil, fmt.Errorf("failed getting start time: '%v'", err)
		}
		buildInfo = BuildInfo{
			GcsPath:   build.GetBuildLogPath(),
			StartTime: startTime,
			Finished:  build.IsFinished(),
		}
		c.mutex.Lock()
		c.buildInfos[buildGcsPath] = buildInfo
		c.mutex.Unlock()
	}
	return &buildInfo, nil
}

func (c *CacheHandler) Save() error {
	if len(c.buildInfos) == 0 {
		return nil
	}
	bucketName, filePath, _ := getBucketAndFilepath(c.cachePath)
	dir, err := ioutil.TempDir("", "")
	if nil != err {
		return err
	}
	srcPath := path.Join(dir, path.Base(c.cachePath))
	var buildInfos []BuildInfo
	for _, buildInfo := range c.buildInfos {
		buildInfos = append(buildInfos, buildInfo)
	}
	contents, err := json.Marshal(buildInfos)
	if nil != err {
		return err
	}
	if err = ioutil.WriteFile(srcPath, contents, 0644); nil != err {
		return err
	}
	return gcs.Upload(ctx, bucketName, filePath, srcPath)
}
