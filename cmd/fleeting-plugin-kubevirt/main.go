package main

import (
	kubevirt "github.com/gonicus/gitlab-fleeting-plugin-kubevirt"
	"gitlab.com/gitlab-org/fleeting/fleeting/plugin"
)

func main() {
	plugin.Main(&kubevirt.InstanceGroup{}, kubevirt.Version)
}
