package ovn_leader_checker

import (
	"github.com/fengjinlin/kube-ovn/pkg/ovn_leader_checker"
	"k8s.io/klog/v2"
	"os"
)

func CmdMain() {
	cfg, err := ovn_leader_checker.ParseFlags()
	if err != nil {
		klog.ErrorS(err, "failed to parse flags")
		os.Exit(1)
	}
	if err = ovn_leader_checker.KubeClientInit(cfg); err != nil {
		klog.ErrorS(err, "failed to initialize kube client")
		os.Exit(1)
	}
	ovn_leader_checker.StartOvnLeaderCheck(cfg)
}
