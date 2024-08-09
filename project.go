package main

import (
	"bytes"
	"fmt"
	"regexp"
	"strings"
	"text/template"

	yaml "gopkg.in/yaml.v2"
)

type PayloadVars struct {
	Tag string
}

func (self PayloadVars) Parse(s string) (string, error) {
	b := bytes.NewBuffer([]byte(""))
	tmpl, err := template.New("").Parse(s)
	if err != nil {
		return "", err
	}
	err = tmpl.Execute(b, self)
	return b.String(), err
}

type DeployPhase struct {
	Name          string      `yaml:"name"`
	Kind          string      `yaml:"kind"`
	Path          string      `yaml:"path"` // for job
	AutoDeploy    bool        `yaml:"autoDeploy"`
	NotifyChannel string      `yaml:"notifyChannel"`
	Payload       string      `yaml:"payload"`
	Destination   Destination `yaml:"destination"`
}

func (p DeployPhase) None() bool {
	return p.Name == ""
}

type DeployProject struct {
	// ID is the name of the configmap that defines the project.
	ID                  string
	Kind                string
	jenkinsJob          string
	funcName            string // for Lambda
	gitHubRepository    string
	defaultBranch       string
	dockerRegistry      string
	filterRegexp        string
	targetRegexp        string
	DisableBranchDeploy bool
	steps               []string
	Alias               string
	Phases              []DeployPhase
}

func (p DeployProject) FindPhase(name string) DeployPhase {
	for _, phase := range p.Phases {
		if phase.Name == name {
			return phase
		}
	}
	return DeployPhase{}
}

func (pj DeployProject) JenkinsJob() string {
	return pj.jenkinsJob
}

func (pj DeployProject) GitHubRepository() string {
	return pj.gitHubRepository
}

func (pj DeployProject) FuncName() string {
	return pj.funcName
}

func (pj DeployProject) Steps() []string {
	return pj.steps
}

// ImageTagRegexp returns the regexp to filter image tags.
// This regexp is used to find the image tag from ECR.
// The regexp is parsed as a template, and the following variables are available:
//
//	{{.Branch}}: The branch name of the target commit.
//	{{.Phase}}: The phase name.
//
// See ECRClient.FindImageTagByRegexp for more details on how the regexp is used.
func (pj DeployProject) ImageTagRegexp() string {
	if pj.filterRegexp == "" {
		return "^{{.Branch}}$"
	}
	return pj.filterRegexp
}

func (pj DeployProject) TargetRegexp() string {
	if pj.targetRegexp == "" {
		return `\b[0-9a-f]{5,40}\b`
	}
	return pj.targetRegexp
}

func (pj DeployProject) DockerRepository() string {
	return pj.dockerRegistry
}

func (pj DeployProject) DefaultBranch() string {
	if pj.defaultBranch == "" {
		return "master"
	}
	return pj.defaultBranch
}

func (pj DeployProject) ECRRepository() string {
	path := strings.Split(pj.dockerRegistry, "/")
	if len(path) < 2 {
		return ""
	}
	return strings.Join(path[1:], "/")
}

func (pj DeployProject) ECRRegistryId() string {
	path := strings.Split(pj.dockerRegistry, ".")
	if len(path) < 2 {
		return ""
	}
	return path[0]
}

type ProjectList struct {
	Items []DeployProject
}

func NewProjectList() (pl ProjectList) {
	pl.Reload()
	return
}

func (p *ProjectList) Reload() {
	var tmp []DeployProject
	cml := getConfigMapList("project")
	for _, cm := range cml.Items {
		pj := DeployProject{}
		pj.ID = cm.Name
		pj.Kind = cm.Data["Kind"]
		pj.jenkinsJob = cm.Data["JenkinsJob"]
		pj.gitHubRepository = cm.Data["GitHubRepository"]
		pj.dockerRegistry = cm.Data["DockerRegistry"]
		pj.defaultBranch = cm.Data["DefaultBranch"]
		pj.filterRegexp = cm.Data["FilterRegexp"]
		pj.targetRegexp = cm.Data["TargetRegexp"]
		pj.funcName = cm.Data["FuncName"]
		pj.Alias = cm.Data["Alias"]
		pj.DisableBranchDeploy = cm.Data["DisableBranchDeploy"] == "true"
		if err := yaml.Unmarshal([]byte(cm.Data["Steps"]), &pj.steps); err != nil {
			fmt.Printf("[ERROR] Failed to parse steps for %s: %s\n", pj.ID, err)
		}
		if err := yaml.Unmarshal([]byte(cm.Data["Phases"]), &pj.Phases); err != nil {
			fmt.Printf("[ERROR] Failed to parse phases for %s: %s\n", pj.ID, err)
		}
		for i, phase := range pj.Phases {
			if phase.Kind == "" {
				pj.Phases[i].Kind = pj.Kind
			}
			if phase.Destination.Kind == "" {
				pj.Phases[i].Destination.Kind = pj.Phases[i].Kind
			}
			if phase.Destination.Kustomize.Path == "" {
				pj.Phases[i].Destination.Kustomize.Path = phase.Path
			}
			if phase.Destination.Kustomize.Image == "" {
				pj.Phases[i].Destination.Kustomize.Image = pj.DockerRepository()
			}
			if phase.Destination.ECS.Image == "" {
				pj.Phases[i].Destination.ECS.Image = pj.DockerRepository()
			}
		}
		tmp = append(tmp, pj)
	}
	p.Items = tmp
}

func (p ProjectList) FindAll(ids []string) (o []DeployProject) {
	for _, id := range ids {
		for _, pj := range p.Items {
			if pj.ID == id {
				o = append(o, pj)
			}
		}
	}
	return o
}

func (p ProjectList) Find(id string) DeployProject {
	for _, pj := range p.Items {
		if pj.ID == id {
			return pj
		}
	}
	fmt.Printf("[ERROR] No Such Project. ID: %s", id)
	return DeployProject{}
}

func (p ProjectList) FindByAlias(id string) (DeployProject, error) {
	for _, pj := range p.Items {
		if regexp.MustCompile(pj.Alias).Match([]byte(id)) {
			return pj, nil
		}
	}
	return DeployProject{}, fmt.Errorf("[ERROR] No Such Project. ID: %s", id)
}
