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
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// update all tags in a byte slice
func updateAllTags(pv *PRVersions, content []byte, imageFilter *regexp.Regexp) ([]byte, string) {
	var msg string
	indexes := imageRegexp.FindAllSubmatchIndex(content, -1)
	// Not finding any images is not an error.
	if indexes == nil {
		return content, msg
	}

	var res string
	lastIndex := 0
	for _, m := range indexes {
		res += string(content[lastIndex : m[imageImagePart*2+1]+1])
		image := string(content[m[imageImagePart*2]:m[imageImagePart*2+1]])
		tag := string(content[m[imageTagPart*2]:m[imageTagPart*2+1]])
		lastIndex = m[1]

		// if tag == "" || (imageFilter != nil && !imageFilter.MatchString(image+":"+tag)) {
		// 	newContent = append(newContent, content[m[imageTagPart*2]:m[1]]...)
		// 	continue
		// }

		iv := pv.getIndex(image, tag)
		if "" != pv.images[image][iv].newVersion {
			res += pv.images[image][iv].newVersion
			msg += fmt.Sprintf("\nImage: %s\nOld Tag: %s\nNew Tag: %s", image, tag, pv.images[image][iv].newVersion)
		} else {
			log.Printf("Cannot find version for image: '%s:%s'.\n", image, tag)
			res += tag
		}
	}
	res += string(content[lastIndex:])

	return []byte(res), msg
}

// UpdateFile updates a file in place.
func UpdateFile(pv *PRVersions, fp string, imageFilter *regexp.Regexp, dryrun bool) error {
	content, err := ioutil.ReadFile(fp)
	if err != nil {
		return fmt.Errorf("failed to read %s: %v", fp, err)
	}

	newContent, msg := updateAllTags(pv, content, imageFilter)

	if err := run(
		fmt.Sprintf("Update file '%s':%s", fp, msg),
		func() error {
			return ioutil.WriteFile(fp, newContent, 0644)
		},
		dryrun); err != nil {
		return fmt.Errorf("failed to write %s: %v", fp, err)
	}
	return nil
}

func cdToRootDir() error {
	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	output, err := cmd.Output()
	if err != nil {
		return err
	}
	d := strings.TrimSpace(string(output))
	log.Println("Changing working directory to %s...", d)
	return os.Chdir(d)
}

func call(cmd string, args ...string) error {
	c := exec.Command(cmd, args...)
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	return c.Run()
}

func updateReferences(pv *PRVersions, dryrun bool) error {
	err := filepath.Walk(".", func(fp string, info os.FileInfo, err error) error {
		if strings.HasSuffix(fp, ".yaml") {
			if err := UpdateFile(pv, fp, imageRegexp, dryrun); err != nil {
				return fmt.Errorf("Failed to update path %s '%v'", fp, err)
			}
		}
		return nil
	})
	if err != nil {
		return err
	}

	return nil
}

func updateVersions(pv *PRVersions, dryrun bool) error {
	if err := cdToRootDir(); err != nil {
		return fmt.Errorf("failed to change to root dir")
	}
	return updateReferences(pv, dryrun)
}
