package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	"github.com/zaiminc/gocat/slackcmd"
)

// SlackListener is a http.Handler that can handle slack events.
// See https://api.slack.com/apis/connections/events-api for more details about events.
type SlackListener struct {
	client            *slack.Client
	verificationToken string
	projectList       *ProjectList
	userList          *UserList
	interactorFactory *InteractorFactory
}

func (s SlackListener) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	buf := new(bytes.Buffer)
	if _, err := buf.ReadFrom(r.Body); err != nil {
		fmt.Printf("[ERROR] Failed to read request body: %s", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	body := buf.String()
	header := r.Header

	if header.Get("X-Slack-Retry-Num") != "" {
		slackRetryNum, _ := strconv.Atoi(header.Get("X-Slack-Retry-Num"))
		if slackRetryNum > 0 {
			return
		}
	}

	eventsAPIEvent, err := slackevents.ParseEvent(json.RawMessage(body), slackevents.OptionVerifyToken(&slackevents.TokenComparator{VerificationToken: s.verificationToken}))
	if err != nil {
		fmt.Println(err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	if eventsAPIEvent.Type == slackevents.URLVerification {
		var r *slackevents.ChallengeResponse
		err = json.Unmarshal([]byte(body), &r)
		if err != nil {
			fmt.Println(err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text")
		if _, err := w.Write([]byte(r.Challenge)); err != nil {
			fmt.Printf("[ERROR] Failed to write challenge response: %s", err)
			w.WriteHeader(http.StatusInternalServerError)
		}
		return
	}

	if eventsAPIEvent.Type == slackevents.CallbackEvent {
		innerEvent := eventsAPIEvent.InnerEvent
		switch ev := innerEvent.Data.(type) {
		case *slackevents.AppMentionEvent:
			if err := s.handleMessageEvent(ev); err != nil {
				log.Println("[ERROR] ", err)
				w.WriteHeader(http.StatusInternalServerError)
			}
		}
	}
}

func (s *SlackListener) handleMessageEvent(ev *slackevents.AppMentionEvent) error {
	// Only response mention to bot. Ignore else.
	log.Print(ev.Text)
	if regexp.MustCompile(`help`).MatchString(ev.Text) {
		if _, _, err := s.client.PostMessage(ev.Channel, s.helpMessage()); err != nil {
			log.Println("[ERROR] ", err)
		}
		return nil
	}
	if regexp.MustCompile(`ls`).MatchString(ev.Text) {
		if _, _, err := s.client.PostMessage(ev.Channel, s.projectListMessage()); err != nil {
			log.Println("[ERROR] ", err)
		}
		return nil
	}
	if regexp.MustCompile(`reload`).MatchString(ev.Text) {
		s.projectList.Reload()
		s.userList.Reload()
		section := slack.NewSectionBlock(slack.NewTextBlockObject("mrkdwn", "Deploy Projects and Users is Reloaded", false, false), nil, nil)
		if _, _, err := s.client.PostMessage(ev.Channel, slack.MsgOptionBlocks(section)); err != nil {
			log.Println("[ERROR] ", err)
		}
		return nil
	}

	s.projectList.Reload()
	s.userList.Reload()
	if match := regexp.MustCompile(`deploy ([0-9a-zA-Z-]+) (staging|production|sandbox|stg|pro|prd) branch`).FindAllStringSubmatch(ev.Text, -1); match != nil {
		log.Println("[INFO] Deploy command is Called")
		commands := strings.Split(match[0][0], " ")
		target, err := s.projectList.FindByAlias(commands[1])
		if err != nil {
			log.Println("[ERROR] ", err)
			if _, _, err := s.client.PostMessage(ev.Channel, s.errorMessage(err.Error())); err != nil {
				log.Println("[ERROR] ", err)
			}
			return nil
		}

		phase := s.toPhase(commands[2])
		interactor := s.interactorFactory.Get(target, phase)
		blocks, err := interactor.BranchList(target, phase)
		if err != nil {
			log.Println("[ERROR] ", err)
			if _, _, err := s.client.PostMessage(ev.Channel, s.errorMessage(err.Error())); err != nil {
				log.Println("[ERROR] ", err)
			}
			return nil
		}

		if _, _, err := s.client.PostMessage(ev.Channel, slack.MsgOptionBlocks(blocks...)); err != nil {
			log.Println("[ERROR] ", err)
		}
		return nil
	}
	if match := regexp.MustCompile(`deploy ([0-9a-zA-Z-]+) (staging|production|sandbox|stg|pro|prd)`).FindAllStringSubmatch(ev.Text, -1); match != nil {
		log.Println("[INFO] Deploy command is Called")
		commands := strings.Split(match[0][0], " ")
		target, err := s.projectList.FindByAlias(commands[1])
		if err != nil {
			log.Println("[ERROR] ", err)
			if _, _, err := s.client.PostMessage(ev.Channel, s.errorMessage(err.Error())); err != nil {
				log.Println("[ERROR] ", err)
			}
			return nil
		}

		phase := s.toPhase(commands[2])
		interactor := s.interactorFactory.Get(target, phase)
		blocks, err := interactor.Request(target, phase, target.DefaultBranch(), ev.User, ev.Channel)
		if err != nil {
			log.Println("[ERROR] ", err)
			if _, _, err := s.client.PostMessage(ev.Channel, s.errorMessage(err.Error())); err != nil {
				log.Println("[ERROR] ", err)
			}
			return nil
		}

		if _, _, err := s.client.PostMessage(ev.Channel, slack.MsgOptionBlocks(blocks...)); err != nil {
			log.Println("[ERROR] ", err)
		}
		return nil
	}
	if regexp.MustCompile(`deploy staging`).MatchString(ev.Text) {
		msgOpt := s.SelectDeployTarget("staging")
		if _, _, err := s.client.PostMessage(ev.Channel, msgOpt); err != nil {
			log.Println("[ERROR] ", err)
		}
		return nil
	}
	if regexp.MustCompile(`deploy production`).MatchString(ev.Text) {
		msgOpt := s.SelectDeployTarget("production")
		if _, _, err := s.client.PostMessage(ev.Channel, msgOpt); err != nil {
			log.Println("[ERROR] ", err)
		}
		return nil
	}
	if regexp.MustCompile(`deploy sandbox`).MatchString(ev.Text) {
		msgOpt := s.SelectDeployTarget("sandbox")
		if _, _, err := s.client.PostMessage(ev.Channel, msgOpt); err != nil {
			log.Println("[ERROR] ", err)
		}
		return nil
	}
	if cmd, _ := slackcmd.Parse(ev.Text); cmd != nil {
		log.Printf("[INFO] %s command is Called", cmd.Name())
		// TODO run the command and post the result to slack
		return nil
	}
	return nil
}

func (s *SlackListener) helpMessage() slack.MsgOption {
	deployMasterText := slack.NewTextBlockObject("mrkdwn", "*masterのデプロイ*\n`@bot-name deploy api staging`\napiの部分はその他アプリケーションに置換可能です。stagingの部分はproductionやsandboxに置換可能です。\nコマンド入力後にデプロイするかの確認ボタンが出てきます。", false, false)
	deployMasterSection := slack.NewSectionBlock(deployMasterText, nil, nil)

	deployBranchText := slack.NewTextBlockObject("mrkdwn", "*ブランチのデプロイ*\n`@bot-name deploy api staging branch`\napiの部分はその他アプリケーションに置換可能です。stagingの部分はproductionやsandboxに置換可能です。\nブランチを選択するドロップダウンが出てきます。\nブランチ選択後にデプロイするかの確認ボタンが出てきます。", false, false)
	deployBranchSection := slack.NewSectionBlock(deployBranchText, nil, nil)

	deployText := slack.NewTextBlockObject("mrkdwn", "*デプロイ対象の選択をSlackのUIから選択するデプロイ手法*\n`@bot-name deploy staging`\nstagingの部分はproductionやsandboxに置換可能です。\nデプロイ対象の選択後にデプロイするブランチの選択肢が出てきます。", false, false)
	deploySection := slack.NewSectionBlock(deployText, nil, nil)

	return slack.MsgOptionBlocks(
		deployMasterSection,
		deployBranchSection,
		deploySection,
		CloseButton(),
	)
}

func (s *SlackListener) projectListMessage() slack.MsgOption {
	text := ""
	for _, pj := range s.projectList.Items {
		text = text + fmt.Sprintf("*%s* (%s)\n", pj.ID, pj.GitHubRepository())
	}

	listText := slack.NewTextBlockObject("mrkdwn", text, false, false)
	listSection := slack.NewSectionBlock(listText, nil, nil)

	return slack.MsgOptionBlocks(
		listSection,
		CloseButton(),
	)
}

// SelectDeployTarget デプロイ対象を選択するボタンを表示する
func (s *SlackListener) SelectDeployTarget(phase string) slack.MsgOption {
	headerText := slack.NewTextBlockObject("mrkdwn", ":cat:", false, false)
	headerSection := slack.NewSectionBlock(headerText, nil, nil)
	sections := make([]slack.Block, len(s.projectList.Items)+2)
	sections[0] = headerSection
	for i, pj := range s.projectList.Items {
		sections[i+1] = createDeployButtonSection(pj, phase)
	}
	sections[len(sections)-1] = CloseButton()
	return slack.MsgOptionBlocks(sections...)
}

func createDeployButtonSection(pj DeployProject, phaseName string) *slack.SectionBlock {
	action := "branchlist"
	if pj.DisableBranchDeploy {
		action = "request"
	}
	phase := pj.FindPhase(phaseName)
	txt := slack.NewTextBlockObject("mrkdwn", fmt.Sprintf("*%s* (%s)", pj.ID, pj.GitHubRepository()), false, false)
	btnTxt := slack.NewTextBlockObject("plain_text", "Deploy", false, false)
	btn := slack.NewButtonBlockElement("", fmt.Sprintf("deploy_%s_%s|%s_%s", phase.Kind, action, pj.ID, phase.Name), btnTxt)
	section := slack.NewSectionBlock(txt, nil, slack.NewAccessory(btn))
	return section
}

func (s *SlackListener) errorMessage(message string) slack.MsgOption {
	txt := slack.NewTextBlockObject("mrkdwn", message, false, false)
	section := slack.NewSectionBlock(txt, nil, nil)
	return slack.MsgOptionBlocks(section)
}

func (s *SlackListener) toPhase(str string) string {
	switch str {
	case "pro", "prd", "production":
		return "production"
	case "stg", "staging":
		return "staging"
	case "sandbox":
		return "sandbox"
	default:
		return "staging"
	}
}
