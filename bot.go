package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"sync"

	"github.com/slack-go/slack"
)

func main() {
	config, err := InitConfig()
	if err != nil {
		log.Fatal(err)
	}

	client := slack.New(
		config.SlackOAuthToken,
		slack.OptionLog(log.New(os.Stdout, "slack-bot: ", log.Lshortfile|log.LstdFlags)),
	)
	github := CreateGitHubInstance("", config.GitHubAccessToken, config.ManifestRepositoryOrg, config.ManifestRepositoryName, config.GitHubDefaultBranch)
	git := CreateGitOperatorInstance(
		config.GitHubUserName,
		config.GitHubAccessToken,
		config.ManifestRepository,
		config.GitHubDefaultBranch,
		os.Getenv("GOCAT_GITROOT"),
	)
	userList := UserList{github: github, slackClient: client}
	projectList := NewProjectList()
	interactorContext := InteractorContext{projectList: &projectList, userList: &userList, github: github, git: git, client: client, config: *config}
	interactorFactory := NewInteractorFactory(interactorContext)
	autoDeploy := NewAutoDeploy(client, &github, &git, &projectList)

	log.SetOutput(os.Stdout)
	if config.EnableAutoDeploy {
		autoDeploy.Watch(60)
	}

	http.Handle("/events", SlackListener{
		client:            client,
		verificationToken: config.SlackVerificationToken,
		projectList:       &projectList,
		userList:          &userList,
		interactorFactory: &interactorFactory,
		mu:                &sync.Mutex{},
	})
	http.Handle("/interaction", interactionHandler{
		verificationToken: config.SlackVerificationToken,
		client:            client,
		projectList:       &projectList,
		userList:          &userList,
		interactorFactory: &interactorFactory,
	})
	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "hello")
	})

	// As the Go documentation says, ListenAndServe always returns a non-nil error,
	// and the error is usually ErrServerClosed on graceful stop.
	//
	// Therefore, we exit with 0 when the error is ErrServerClosed,
	// and log the error then exit with 1 otherwise for diagnosis.
	if err := http.ListenAndServe(":3000", nil); err != http.ErrServerClosed {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
