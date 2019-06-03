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

// prow-auto-bumper finds stable Prow components version used by k8s,
// and creates PRs updating them in knative/test-infra

package main

import (
	"flag"
	"fmt"
	"log"
	"regexp"
	"time"

	"github.com/google/go-github/github"
	"github.com/knative/test-infra/shared/ghutil"
)

const (
	srcOrg  = "kubernetes"
	srcRepo = "test-infra"
	// PRHead is the head branch of k8s auto version bump PRs
	// TODO(chaodaiG): using head branch querying is less ideal than using
	// label `area/prow/bump`, which is not supported by Github API yet. Move
	// to filter using this label once it's supported
	srcPRHead = "autobump"
	// PRBase is the base branch of k8s auto version bump PRs
	srcPRBase = "master"
	// PRUser is the user from which PR was created
	srcPRUser = "k8s-ci-robot"

	org    = "chaodaiG"
	repo   = "test-infra"
	PRHead = "autobump"
	PRBase = "master"
	// Index for regex matching groups
	imageImagePart = 1
	imageTagPart   = 2
	// Max difference away from target date
	maxDelta = 2 * 24 // 2 days
	// Safe duration is the smallest amount of hours a version stayed
	safeDuration = 12 // 12 hours
	maxRetry     = 3

	oncallAddress = "https://storage.googleapis.com/knative-infra-oncall/oncall.json"
)

var (
	// matching            gcr.io /k8s-(prow|testimage)/(tide|kubekin-e2e|.*)    :vYYYYMMDD-HASH-VARIANT
	imagePattern     = `\b(gcr\.io/k8s[a-z0-9-]{5,29}/[a-zA-Z0-9][a-zA-Z0-9_.-]+):(v[a-zA-Z0-9_.-]+)\b`
	imageRegexp      = regexp.MustCompile(imagePattern)
	imageLinePattern = fmt.Sprintf(`\s+[a-z]+:\s+"?'?%s"?'?`, imagePattern)
	// matching   "-    image: gcr.io /k8s-(prow|testimage)/(tide|kubekin-e2e|.*)    :vYYYYMMDD-HASH-VARIANT"
	imageMinusRegexp = regexp.MustCompile(fmt.Sprintf(`\-%s`, imageLinePattern))
	// matching   "+    image: gcr.io /k8s-(prow|testimage)/(tide|kubekin-e2e|.*)    :vYYYYMMDD-HASH-VARIANT"
	imagePlusRegexp = regexp.MustCompile(fmt.Sprintf(`\+%s`, imageLinePattern))
	// Preferred time for candidate PR creation date
	targetTime = time.Now().Add(-time.Hour * 7 * 24) // 7 days
)

// GHClientWrapper handles methods for github issues
type GHClientWrapper struct {
	ghutil.GithubOperations
}

type gitInfo struct {
	org   string
	repo  string
	head  string
	base  string
	user  string
	email string
}

// versions holds the version change for an image
// oldVersion and newVersion are both in the format of "vYYYYMMDD-HASH-VARIANT"
type versions struct {
	oldVersion string
	newVersion string
	variant    string
}

// PRVersions contains PR and version changes in it
type PRVersions struct {
	images map[string][]versions // map of image name: versions struct
	// The way k8s updates versions doesn't guarantee the same version tag across all images,
	// dominantVersions is the version that appears most times
	dominantVersions *versions
	PR               *github.PullRequest
}

func main() {
	githubAccount := flag.String("github-account", "", "Token file for Github authentication")
	gitUser := flag.String("git-user", "", "The name to use on the git commit. Requires --git-email")
	gitEmail := flag.String("git-email", "", "The email to use on the git commit. Requires --git-name")
	dryrun := flag.Bool("dry-run", false, "dry run switch")
	flag.Parse()

	if nil != dryrun && true == *dryrun {
		log.Printf("running in [dry run mode]")
	}

	if nil == gitUser || "" == *gitUser {
		log.Fatalf("git-user must be provided")
	}

	gc, err := ghutil.NewGithubClient(*githubAccount)
	if nil != err {
		log.Fatalf("cannot authenticate to github: %v", err)
	}

	srcGI := gitInfo{
		org:  srcOrg,
		repo: srcRepo,
		head: srcPRHead,
		base: srcPRBase,
		user: srcPRUser,
	}

	targetGI := gitInfo{
		org:   org,
		repo:  repo,
		head:  PRHead,
		base:  PRBase,
		user:  *gitUser,
		email: *gitEmail,
	}

	gcw := &GHClientWrapper{gc}
	bestVersion, err := retryGetBestVersion(gcw, srcGI)
	if nil != err {
		log.Fatalf("cannot get best version from %s/%s: '%v'", srcGI.org, srcGI.repo, err)
	}

	log.Println(bestVersion.dominantVersions)

	if err = updateVersions(bestVersion, *dryrun); nil != err {
		log.Fatalf("failed updating versions: '%v'", err)
	}

	if err = createOrUpdatePR(gcw, bestVersion, targetGI, *dryrun); nil != err {
		log.Fatalf("failed creating pullrequest: '%v'", err)
	}
}
