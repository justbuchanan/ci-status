package main

import (
	"context"
	"flag"
	"fmt"
	"github.com/google/go-github/github"
	"golang.org/x/oauth2"
	"io"
	"log"
	"os"
	"os/exec"
	"path"
	"strings"
)

var (
	token                 string
	username              string
	repo                  string
	rev                   string
	verbose               bool
	fakeGithubInteraction bool
	dontPrintCommand      bool
	status                = &github.RepoStatus{
		TargetURL:   new(string),
		State:       new(string),
		Description: new(string),
		Context:     new(string),
	}
	artifactsDir string
)

func loadCircleEnv(artifactName string) {
	defaultToEnv(&username, "CIRCLE_PROJECT_USERNAME")
	defaultToEnv(&repo, "CIRCLE_PROJECT_REPONAME")
	defaultToEnv(&rev, "CIRCLE_SHA1")
	defaultToEnv(&artifactsDir, "CIRCLE_ARTIFACTS")

	if status.GetTargetURL() == "" {
		// "https://circleci.com/api/v1.1/project/:vcs-type/:org-name/:repo-name/:build_num/artifacts/:container-index/path/to/artifact"
		// TODO: bitbucket?
		buildNum := os.Getenv("CIRCLE_BUILD_NUM")
		nodeIndex := os.Getenv("CIRCLE_NODE_INDEX")
		*status.TargetURL = fmt.Sprintf("https://circleci.com/api/v1.1/project/github/%s/%s/%s/artifacts/%s%s/%s", username, repo, buildNum, nodeIndex, artifactsDir, artifactName)
	}
}

func loadTravisEnv() {
	parts := strings.Split(os.Getenv("TRAVIS_REPO_SLUG"), "/")
	if username == "" {
		username = parts[0]
	}
	if repo == "" {
		repo = parts[1]
	}

	defaultToEnv(&rev, "TRAVIS_COMMIT")
	// TODO: artifacts directory

	*status.TargetURL = fmt.Sprintf("https://travis-ci.org/justbuchanan/ci-test/builds/%s", os.Getenv("TRAVIS_BUILD_ID"))
}

func main() {
	// Follow these directions to create a token:
	// https://help.github.com/articles/creating-a-personal-access-token-for-the-command-line/
	flag.StringVar(&token, "token", "", "GitHub api token. Should be restricted to repo:status scope.")
	flag.StringVar(&username, "username", "", "Username")
	flag.StringVar(&repo, "repo", "", "Repository name.")
	flag.StringVar(&rev, "rev", "", "Git commit/revision specifier")
	flag.BoolVar(&verbose, "verbose", false, "extra logging")
	flag.BoolVar(&fakeGithubInteraction, "fake_github", false, "For testing purposes, don't talk to github, just log actions.")
	showOutputFlag := flag.Bool("show_output", true, "display command output")
	flag.BoolVar(&dontPrintCommand, "h", false, "Don't print command in the output. Use this if it contains secret tokens, etc.")

	flag.StringVar(status.TargetURL, "target_url", "", "Url that this status should redirect to.")
	flag.StringVar(status.Context, "context", "status", "Unique string identifier for this status. Something like 'compile', 'test', or 'deploy'.")
	flag.StringVar(status.Description, "description", "", "Description of the test, etc.")
	flag.StringVar(&artifactsDir, "artifacts-dir", "", "Artifacts directory.")

	flag.Parse()

	// the last arg is the command to run
	taskCmd := flag.Arg(0)
	if taskCmd == "" {
		log.Fatal("No command provided")
	}

	if status.GetDescription() == "" {
		log.Fatal("Please provide a description")
	}

	logfileName := status.GetContext() + ".txt"

	artifactsDir := ""

	// get parameters from environment variables if they weren't given in args
	if !fakeGithubInteraction {
		defaultToEnv(&token, "GITHUB_API_TOKEN")
	}
	if os.Getenv("CI") == "true" {
		if os.Getenv("CIRCLECI") == "true" {
			loadCircleEnv(logfileName)
		} else if os.Getenv("TRAVIS") == "true" {
			loadTravisEnv()
		} else {
			log.Print("CI not recognized, continuing anyways.")
		}
	} else {
		log.Print("No CI detected, continuing anyways.")
	}

	// TODO: validate params

	ctx := context.Background()
	ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: token})
	tc := oauth2.NewClient(ctx, ts)
	client := github.NewClient(tc)

	*status.State = "pending"
	postStatus(client, ctx)

	// open logfile
	logfilePath := path.Join(artifactsDir, logfileName)
	logfile, err := os.Create(logfilePath)
	if err != nil {
		log.Fatal(err)
	}
	defer logfile.Close()

	// create command, logging outputs to logfile
	cmd := exec.Command("bash", "-c", taskCmd)

	if *showOutputFlag {
		// print command output to logfile and stderr
		mw := io.MultiWriter(os.Stderr, logfile)

		cmd.Stdout = mw
		cmd.Stderr = mw
	} else {
		cmd.Stdout = logfile
		cmd.Stderr = logfile
	}

	if !dontPrintCommand {
		log.Println("Running task command:", taskCmd)
	} else {
		log.Println("Running task command")
	}
	log.Println("Logging to", logfilePath)

	// run it!
	err = cmd.Run()

	// check status
	if err != nil {
		log.Println(err)
		*status.State = "failure"
	} else {
		*status.State = "success"
	}

	// post final status to github
	postStatus(client, ctx)

	if status.GetState() == "failure" {
		os.Exit(1)
	}
}

func postStatus(client *github.Client, ctx context.Context) {
	if fakeGithubInteraction {
		log.Printf("[Fake Github] Updating status for '%s' to %s", status.GetContext(), status.GetState())
		return
	}
	ss, resp, err := client.Repositories.CreateStatus(ctx, username, repo, rev, status)
	if err != nil {
		log.Fatal(err)
	}
	if verbose {
		log.Println(resp)
		log.Println(ss)
	}
	log.Printf("Updated status for '%s' to '%s'\n", status.GetContext(), status.GetState())
}

func defaultToEnv(val *string, envName string) {
	if *val != "" {
		return
	}

	*val = os.Getenv(envName)
	if *val == "" {
		log.Fatalf("No value for %s, aborting...\n", envName)
	}
}
