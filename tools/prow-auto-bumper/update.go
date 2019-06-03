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

	"github.com/knative/test-infra/shared/ghutil"

	"github.com/github/hub/github"
	"github.com/kubernetes/test-infra/robots/pr-creator/updater"
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

func (c *Client) getExistingPR(title string) *ghutil.PullRequest {
	return nil
}

func (c *Client) updatePR(gc github.Client, org, repo, title, body, matchTitle, source, branch string) error {
	logrus.Info("Creating PR...")
	if existingPR := c.getExistingPR(matchTitle); nil != existingPR {
		return c.githubClient.UpdatePullRequest(c.org, c.repo, *existingPR.Number, title, body)
	}

	_, err := c.githubClient.CreatePullRequest(org, repo, title, body, source, branch, true)
	if err != nil {
		return fmt.Errorf("failed to create PR: %v", err)
	}

	return nil
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

func makeCommitSummary(vs versions) string {
	return fmt.Sprintf("Update prow from %s to %s, and other images as necessary.", vs.oldVersion, vs.newVersion)
}

func makeGitCommit(user, branch, name, email string, dominantVs versions, dryrun bool) error {
	if "" == branch {
		log.Fatal("pushing to empty branch ref is not allowed")
	}
	if err := run(
		"Running 'git add -A'",
		func() error { return call("git", "add", "-A") },
		dryrun); err != nil {
		return fmt.Errorf("failed to git add: %v", err)
	}
	message := makeCommitSummary(dominantVs)
	commitArgs := []string{"commit", "-m", message}
	if name != "" && email != "" {
		commitArgs = append(commitArgs, "--author", fmt.Sprintf("%s <%s>", name, email))
	}
	if err := run(
		fmt.Sprintf("Running 'git %s'", strings.Join(commitArgs, " ")),
		func() error { return call("git", commitArgs...) },
		dryrun); err != nil {
		return fmt.Errorf("failed to git commit: %v", err)
	}
	pushArgs := []string{"push", "-f", fmt.Sprintf("git@github.com:%s/test-infra.git", user), fmt.Sprintf("HEAD:%s", branch)}
	if err := run(
		fmt.Sprintf("Running 'git %s'", strings.Join(pushArgs, " ")),
		func() error { return call("git", pushArgs...) },
		dryrun); err != nil {
		return fmt.Errorf("failed to git push: %v", err)
	}
	return nil
}

// func tagFromName(name string) string {
// 	parts := strings.Split(name, ":")
// 	if len(parts) < 2 {
// 		return ""
// 	}
// 	return parts[1]
// }

// func componentFromName(name string) string {
// 	s := strings.Split(strings.Split(name, ":")[0], "/")
// 	return s[len(s)-1]
// }

// func formatTagDate(d string) string {
// 	if len(d) != 8 {
// 		return d
// 	}
// 	// &#x2011; = U+2011 NON-BREAKING HYPHEN, to prevent line wraps.
// 	return fmt.Sprintf("%s&#x2011;%s&#x2011;%s", d[0:4], d[4:6], d[6:8])
// }

// func generateSummary(name, repo, prefix string, summarise bool, images map[string]string) string {
// 	type delta struct {
// 		oldCommit string
// 		newCommit string
// 		oldDate   string
// 		newDate   string
// 		variant   string
// 		component string
// 	}
// 	versions := map[string][]delta{}
// 	for image, newTag := range images {
// 		if !strings.HasPrefix(image, prefix) {
// 			continue
// 		}
// 		if strings.HasSuffix(image, ":"+newTag) {
// 			continue
// 		}
// 		oldDate, oldCommit, oldVariant := bumper.DeconstructTag(tagFromName(image))
// 		newDate, newCommit, _ := bumper.DeconstructTag(newTag)
// 		k := oldCommit + ":" + newCommit
// 		d := delta{
// 			oldCommit: oldCommit,
// 			newCommit: newCommit,
// 			oldDate:   oldDate,
// 			newDate:   newDate,
// 			variant:   oldVariant,
// 			component: componentFromName(image),
// 		}
// 		versions[k] = append(versions[k], d)
// 	}

// 	switch {
// 	case len(versions) == 0:
// 		return fmt.Sprintf("No %s changes.", name)
// 	case len(versions) == 1 && summarise:
// 		for k, v := range versions {
// 			s := strings.Split(k, ":")
// 			return fmt.Sprintf("%s changes: %s/compare/%s...%s (%s → %s)", name, repo, s[0], s[1], formatTagDate(v[0].oldDate), formatTagDate(v[0].newDate))
// 		}
// 	default:
// 		changes := make([]string, 0, len(versions))
// 		for k, v := range versions {
// 			s := strings.Split(k, ":")
// 			names := make([]string, 0, len(v))
// 			for _, d := range v {
// 				names = append(names, d.component+d.variant)
// 			}
// 			sort.Strings(names)
// 			changes = append(changes, fmt.Sprintf("%s/compare/%s...%s | %s&nbsp;&#x2192;&nbsp;%s | %s",
// 				repo, s[0], s[1], formatTagDate(v[0].oldDate), formatTagDate(v[0].newDate), strings.Join(names, ", ")))
// 		}
// 		sort.Slice(changes, func(i, j int) bool { return strings.Split(changes[i], "|")[1] < strings.Split(changes[j], "|")[1] })
// 		return fmt.Sprintf("Multiple distinct %s changes:\n\nCommits | Dates | Images\n--- | --- | ---\n%s\n", name, strings.Join(changes, "\n"))
// 	}
// 	panic("unreachable!")
// }

// func getOncaller() (string, error) {
// 	req, err := http.Get(oncallAddress)
// 	if err != nil {
// 		return "", err
// 	}
// 	defer req.Body.Close()
// 	if req.StatusCode != http.StatusOK {
// 		return "", fmt.Errorf("HTTP error %d (%q) fetching current oncaller", req.StatusCode, req.Status)
// 	}
// 	oncall := struct {
// 		Oncall struct {
// 			TestInfra string `json:"testinfra"`
// 		} `json:"Oncall"`
// 	}{}
// 	if err := json.NewDecoder(req.Body).Decode(&oncall); err != nil {
// 		return "", err
// 	}
// 	return oncall.Oncall.TestInfra, nil
// }

// func generatePRBody(images map[string]string) string {
// 	prowSummary := generateSummary("Prow", prowRepo, prowPrefix, true, images)
// 	testImagesSummary := generateSummary("test-image", testImageRepo, testImagePrefix, false, images)
// 	oncaller, err := getOncaller()

// 	var assignment string
// 	if err == nil {
// 		if oncaller != "" {
// 			assignment = "/cc @" + oncaller
// 		} else {
// 			assignment = "Nobody is currently oncall, so falling back to Blunderbuss."
// 		}
// 	} else {
// 		assignment = fmt.Sprintf("An error occurred while finding an assignee: `%s`.\nFalling back to Blunderbuss.", err)
// 	}
// 	body := prowSummary + "\n\n" + testImagesSummary + "\n\n" + assignment + "\n"
// 	return body
// }

func update(pv *PRVersions, gitName, gitEmail string, dryrun bool) {
	if err := cdToRootDir(); err != nil {
		log.Fatal("Failed to change to root dir")
	}
	if err := updateReferences(pv, dryrun); err != nil {
		log.Fatal("Failed to update references.")
	}

	if err := makeGitCommit("chaodaiG", "autobump", gitName, gitEmail, *pv.dominantVs, dryrun); err != nil {
		log.Fatal("Failed to push changes.")
	}

	// if err := updatePR(gc, githubOrg, githubRepo, makeCommitSummary(images), generatePRBody(images), "Update prow to", o.githubLogin+":autobump", "master"); err != nil {
	// 	logrus.WithError(err).Fatal("PR creation failed.")
	// }
}
