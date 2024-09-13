package daemon

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/sirupsen/logrus"
	"github.com/spf13/pflag"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog/v2"

	clientset "github.com/fengjinlin/kube-ovn/pkg/client/clientset/versioned"
	"github.com/fengjinlin/kube-ovn/pkg/consts"
	"github.com/fengjinlin/kube-ovn/pkg/ovs"
	"github.com/fengjinlin/kube-ovn/pkg/utils"
)

const defaultBindSocket = "/run/openvswitch/kube-ovn-daemon.sock"

// Configuration is the daemon conf
type Configuration struct {
	// interface being used for tunnel
	tunnelIface               string
	Iface                     string
	DPDKTunnelIface           string
	MTU                       int
	MSS                       int
	EnableMirror              bool
	MirrorNic                 string
	BindSocket                string
	OvsSocket                 string
	KubeConfigFile            string
	KubeClient                kubernetes.Interface
	KubeOvnClient             clientset.Interface
	NodeName                  string
	ServiceClusterIPRange     string
	ClusterRouter             string
	NodeSwitch                string
	EncapChecksum             bool
	EnablePprof               bool
	MacLearningFallback       bool
	PprofPort                 int
	NetworkType               string
	CniConfDir                string
	CniConfFile               string
	CniConfName               string
	DefaultProviderName       string
	DefaultInterfaceName      string
	ExternalGatewayConfigNS   string
	ExternalGatewaySwitch     string // provider network underlay vlan subnet
	EnableMetrics             bool
	EnableArpDetectIPConflict bool
	KubeletDir                string
	EnableVerboseConnCheck    bool
	TCPConnCheckPort          int
	UDPConnCheckPort          int
	EnableTProxy              bool
	OVSVsctlConcurrency       int32

	vSwitchAddr   string
	vSwitchClient ovs.VSwitchClientInterface
}

// ParseFlags will parse cmd args then init kubeClient and configuration
// TODO: validate configuration
func ParseFlags() *Configuration {
	var (
		argNodeName              = pflag.String("node-name", "", "Name of the node on which the daemon is running on.")
		argIface                 = pflag.String("iface", "", "The iface used to inter-host pod communication, can be a nic name or a group of regex separated by comma (default the default route iface)")
		argDPDKTunnelIface       = pflag.String("dpdk-tunnel-iface", "br-phy", "Specifies the name of the dpdk tunnel iface.")
		argMTU                   = pflag.Int("mtu", 0, "The MTU used by pod iface in overlay networks (default iface MTU - 100)")
		argEnableMirror          = pflag.Bool("enable-mirror", false, "Enable traffic mirror (default false)")
		argMirrorNic             = pflag.String("mirror-iface", "mirror0", "The mirror nic name that will be created by kube-ovn")
		argBindSocket            = pflag.String("bind-socket", defaultBindSocket, "The socket daemon bind to.")
		argOvsSocket             = pflag.String("ovs-socket", "", "The socket to local ovs-server")
		argKubeConfigFile        = pflag.String("kubeconfig", "", "Path to kubeconfig file with authorization and master location information. If not set use the inCluster token.")
		argServiceClusterIPRange = pflag.String("service-cluster-ip-range", "10.96.0.0/12", "The kubernetes service cluster ip range")
		argClusterRouter         = pflag.String("cluster-router", consts.DefaultVpc, "The router name for cluster router")
		argNodeSwitch            = pflag.String("node-switch", "join", "The name of node gateway switch which help node to access pod network")
		argEncapChecksum         = pflag.Bool("encap-checksum", true, "Enable checksum")
		argEnablePprof           = pflag.Bool("enable-pprof", false, "Enable pprof")
		argPprofPort             = pflag.Int("pprof-port", 10665, "The port to get profiling data")
		argMacLearningFallback   = pflag.Bool("mac-learning-fallback", false, "Fallback to the legacy MAC learning mode")

		argsNetworkType              = pflag.String("network-type", consts.NetworkTypeGeneve, "Tunnel encapsulation protocol in overlay networks")
		argCniConfDir                = pflag.String("cni-conf-dir", "/etc/cni/net.d", "Path of the CNI config directory.")
		argCniConfFile               = pflag.String("cni-conf-file", "/kube-ovn/01-kube-ovn.conflist", "Path of the CNI config file.")
		argsCniConfName              = pflag.String("cni-conf-name", "01-kube-ovn.conflist", "Specify the name of kube ovn conflist name in dir /etc/cni/net.d/, default: 01-kube-ovn.conflist")
		argsDefaultProviderName      = pflag.String("default-provider-name", "provider", "The vlan or vxlan type default provider interface name")
		argsDefaultInterfaceName     = pflag.String("default-interface-name", "", "The default host interface name in the vlan/vxlan type")
		argExternalGatewayConfigNS   = pflag.String("external-gateway-config-ns", "kube-system", "The namespace of configmap external-gateway-config, default: kube-system")
		argExternalGatewaySwitch     = pflag.String("external-gateway-switch", "external", "The name of the external gateway switch which is a ovs bridge to provide external network, default: external")
		argEnableMetrics             = pflag.Bool("enable-metrics", true, "Whether to support metrics query")
		argEnableArpDetectIPConflict = pflag.Bool("enable-arp-detect-ip-conflict", true, "Whether to support arp detect ip conflict in vlan network")
		argKubeletDir                = pflag.String("kubelet-dir", "/var/lib/kubelet", "Path of the kubelet dir, default: /var/lib/kubelet")
		argEnableVerboseConnCheck    = pflag.Bool("enable-verbose-conn-check", false, "enable TCP/UDP connectivity check listen port")
		argTCPConnectivityCheckPort  = pflag.Int("tcp-conn-check-port", 8100, "TCP connectivity Check Port")
		argUDPConnectivityCheckPort  = pflag.Int("udp-conn-check-port", 8101, "UDP connectivity Check Port")
		argEnableTProxy              = pflag.Bool("enable-tproxy", false, "enable tproxy for vpc pod liveness or readiness probe")
		argOVSVsctlConcurrency       = pflag.Int32("ovs-vsctl-concurrency", 100, "concurrency limit of ovs-vsctl")

		argVSwitchAddr = pflag.String("vswitch-addr", "unix:/var/run/openvswitch/db.sock", "address of vswitch db")
		//argVSwitchAddr = pflag.String("vswitch-addr", "unix:/usr/local/var/run/openvswitch/db.sock", "address of vswitch db")
	)

	// mute info log for ipset lib
	logrus.SetLevel(logrus.WarnLevel)

	klogFlags := flag.NewFlagSet("klog", flag.ExitOnError)
	klog.InitFlags(klogFlags)

	// Sync the glog and klog flags.
	pflag.CommandLine.VisitAll(func(f1 *pflag.Flag) {
		f2 := klogFlags.Lookup(f1.Name)
		if f2 != nil {
			value := f1.Value.String()
			if err := f2.Value.Set(value); err != nil {
				klog.ErrorS(err, "failed to set flag")
				os.Exit(1)
			}
		}
	})

	pflag.CommandLine.AddGoFlagSet(klogFlags)
	pflag.CommandLine.AddGoFlagSet(flag.CommandLine)
	pflag.Parse()

	config := &Configuration{
		Iface:                     *argIface,
		DPDKTunnelIface:           *argDPDKTunnelIface,
		MTU:                       *argMTU,
		EnableMirror:              *argEnableMirror,
		MirrorNic:                 *argMirrorNic,
		BindSocket:                *argBindSocket,
		OvsSocket:                 *argOvsSocket,
		KubeConfigFile:            *argKubeConfigFile,
		EnablePprof:               *argEnablePprof,
		PprofPort:                 *argPprofPort,
		MacLearningFallback:       *argMacLearningFallback,
		NodeName:                  strings.ToLower(*argNodeName),
		ServiceClusterIPRange:     *argServiceClusterIPRange,
		ClusterRouter:             *argClusterRouter,
		NodeSwitch:                *argNodeSwitch,
		EncapChecksum:             *argEncapChecksum,
		NetworkType:               *argsNetworkType,
		CniConfDir:                *argCniConfDir,
		CniConfFile:               *argCniConfFile,
		CniConfName:               *argsCniConfName,
		DefaultProviderName:       *argsDefaultProviderName,
		DefaultInterfaceName:      *argsDefaultInterfaceName,
		ExternalGatewayConfigNS:   *argExternalGatewayConfigNS,
		ExternalGatewaySwitch:     *argExternalGatewaySwitch,
		EnableMetrics:             *argEnableMetrics,
		EnableArpDetectIPConflict: *argEnableArpDetectIPConflict,
		KubeletDir:                *argKubeletDir,
		EnableVerboseConnCheck:    *argEnableVerboseConnCheck,
		TCPConnCheckPort:          *argTCPConnectivityCheckPort,
		UDPConnCheckPort:          *argUDPConnectivityCheckPort,
		EnableTProxy:              *argEnableTProxy,
		OVSVsctlConcurrency:       *argOVSVsctlConcurrency,

		vSwitchAddr: *argVSwitchAddr,
	}
	return config
}

func (config *Configuration) Init() error {
	if config.NodeName == "" {
		klog.Info("node name not specified in command line parameters, fall back to the environment variable")
		if config.NodeName = strings.ToLower(os.Getenv(consts.HostnameEnv)); config.NodeName == "" {
			klog.Info("node name not specified in environment variables, fall back to the hostname")
			hostname, err := os.Hostname()
			if err != nil {
				return fmt.Errorf("failed to get hostname: %v", err)
			}
			config.NodeName = strings.ToLower(hostname)
		}
	}

	if err := config.initVSwitchClient(); err != nil {
		return err
	}

	if err := config.initKubeClient(); err != nil {
		return err
	}

	klog.Infof("daemon config: %v", config)
	return nil
}

func (config *Configuration) initVSwitchClient() error {
	var err error
	config.vSwitchClient, err = ovs.NewVSwitchClient(config.vSwitchAddr, 60)
	return err
}

func (config *Configuration) initKubeClient() error {
	var cfg *rest.Config
	var err error
	if config.KubeConfigFile == "" {
		klog.Infof("no --kubeconfig, use in-cluster kubernetes config")
		cfg, err = rest.InClusterConfig()
		if err != nil {
			klog.Errorf("use in cluster config failed %v", err)
			return err
		}
	} else {
		cfg, err = clientcmd.BuildConfigFromFlags("", config.KubeConfigFile)
		if err != nil {
			klog.Errorf("use --kubeconfig %s failed %v", config.KubeConfigFile, err)
			return err
		}
	}

	// try to connect to apiserver's tcp port
	if err = utils.DialAPIServer(cfg.Host); err != nil {
		klog.Errorf("failed to dial apiserver: %v", err)
		return err
	}

	cfg.QPS = 1000
	cfg.Burst = 2000

	kubeOvnClient, err := clientset.NewForConfig(cfg)
	if err != nil {
		klog.Errorf("init kubeovn client failed %v", err)
		return err
	}
	config.KubeOvnClient = kubeOvnClient

	cfg.ContentType = consts.ContentType
	cfg.AcceptContentTypes = consts.AcceptContentTypes
	kubeClient, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		klog.Errorf("init kubernetes client failed %v", err)
		return err
	}
	config.KubeClient = kubeClient
	return nil
}
