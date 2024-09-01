package controller

import (
	"flag"
	"fmt"
	"github.com/fengjinlin/kube-ovn/pkg/consts"
	"github.com/fengjinlin/kube-ovn/pkg/utils"
	"github.com/spf13/pflag"
	gozap "go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"gopkg.in/natefinch/lumberjack.v2"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"os"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

// Configuration is the controller conf
type Configuration struct {
	BindAddress          string
	OvnNbAddr            string
	OvnSbAddr            string
	OvnTimeout           int
	CustCrdRetryMaxDelay int
	CustCrdRetryMinDelay int
	KubeConfigFile       string
	KubeRestConfig       *rest.Config `json:"-"`

	DefaultSubnet                   string
	DefaultSubnetCIDR               string
	DefaultSubnetGateway            string
	DefaultSubnetExcludeIps         string
	DefaultSubnetGatewayCheck       bool
	DefaultSubnetLogicalGateway     bool
	DefaultSubnetU2OInterconnection bool

	DefaultVpc        string
	JoinSubnet        string
	JoinSubnetCIDR    string
	JoinSubnetGateway string

	ServiceClusterIPRange string

	ClusterTCPLoadBalancer         string
	ClusterUDPLoadBalancer         string
	ClusterSctpLoadBalancer        string
	ClusterTCPSessionLoadBalancer  string
	ClusterUDPSessionLoadBalancer  string
	ClusterSctpSessionLoadBalancer string

	PodName      string
	PodNamespace string
	PodNicType   string

	WorkerNum       int
	PprofPort       int
	EnablePprof     bool
	NodePgProbeTime int

	NetworkType             string
	DefaultProviderName     string
	DefaultHostInterface    string
	DefaultExchangeLinkName bool
	DefaultVlanName         string
	DefaultVlanID           int
	LsDnatModDlDst          bool
	LsCtSkipDstLportIPs     bool

	EnableLb          bool
	EnableNP          bool
	EnableEipSnat     bool
	EnableExternalVpc bool
	EnableEcmp        bool
	EnableKeepVMIP    bool
	EnableLbSvc       bool
	EnableMetrics     bool

	ExternalGatewaySwitch   string
	ExternalGatewayConfigNS string
	ExternalGatewayNet      string
	ExternalGatewayVlanID   int

	GCInterval      int
	InspectInterval int

	BfdMinTx      int
	BfdMinRx      int
	BfdDetectMult int

	NodeLocalDNSIP string

	LogFile        string
	LogFileMaxsize int
	LogFileMaxAge  int

	MetricsAddr          string
	SecureMetrics        bool
	EnableLeaderElection bool
	ProbeAddr            string
	EnableHTTP2          bool
}

// ParseFlags parses cmd args then init kubeclient and conf
// TODO: validate configuration
func ParseFlags() (*Configuration, error) {
	var (
		argOvnNbAddr            = pflag.String("ovn-nb-addr", "", "ovn-nb address")
		argOvnSbAddr            = pflag.String("ovn-sb-addr", "", "ovn-sb address")
		argOvnTimeout           = pflag.Int("ovn-timeout", 60, "")
		argCustCrdRetryMinDelay = pflag.Int("cust-crd-retry-min-delay", 1, "The min delay seconds between custom crd two retries")
		argCustCrdRetryMaxDelay = pflag.Int("cust-crd-retry-max-delay", 20, "The max delay seconds between custom crd two retries")
		argKubeConfigFile       = pflag.String("kubeconfig", "", "Path to kubeconfig file with authorization and master location information. If not set use the inCluster token.")

		argDefaultSubnet               = pflag.String("default-subnet", consts.DefaultSubnet, "The default subnet name")
		argDefaultSubnetCIDR           = pflag.String("default-subnet-cidr", "10.244.0.0/16", "Default subnet CIDR for namespace with no logical switch annotation")
		argDefaultSubnetGateway        = pflag.String("default-subnet-gateway", "", "Default subnet gateway for default-subnet-cidr (default the first ip in default-cidr)")
		argDefaultSubnetGatewayCheck   = pflag.Bool("default-subnet-gateway-check", true, "Check switch for the default subnet's gateway")
		argDefaultSubnetLogicalGateway = pflag.Bool("default-subnet-logical-gateway", false, "Create a logical gateway for the default subnet instead of using underlay gateway. Take effect only when the default subnet is in underlay mode. (default false)")
		argDefaultSubnetExcludeIps     = pflag.String("default-subnet-exclude-ips", "", "Exclude ips in default switch (default gateway address)")

		argDefaultSubnetU2OInterconnection = pflag.Bool("default-subnet-u2o-interconnection", false, "usage for underlay to overlay interconnection")

		argDefaultVpc        = pflag.String("default-vpc", consts.DefaultVpc, "The router name for cluster router")
		argJoinSubnet        = pflag.String("join-subnet", "join", "The name of join subnet which help node to access pod network")
		argJoinSubnetCIDR    = pflag.String("join-subnet-cidr", "100.64.0.0/16", "The cidr for join subnet")
		argJoinSubnetGateway = pflag.String("join-subnet-gateway", "", "The gateway for join subnet (default the first ip in join-subnet-cidr)")

		argServiceClusterIPRange = pflag.String("service-cluster-ip-range", "10.96.0.0/12", "The kubernetes service cluster ip range")

		argClusterTCPLoadBalancer         = pflag.String("cluster-tcp-loadbalancer", "cluster-tcp-lb", "The name for cluster tcp loadbalancer")
		argClusterUDPLoadBalancer         = pflag.String("cluster-udp-loadbalancer", "cluster-udp-lb", "The name for cluster udp loadbalancer")
		argClusterSctpLoadBalancer        = pflag.String("cluster-sctp-loadbalancer", "cluster-sctp-lb", "The name for cluster sctp loadbalancer")
		argClusterTCPSessionLoadBalancer  = pflag.String("cluster-tcp-session-loadbalancer", "cluster-tcp-session-lb", "The name for cluster tcp session loadbalancer")
		argClusterUDPSessionLoadBalancer  = pflag.String("cluster-udp-session-loadbalancer", "cluster-udp-session-lb", "The name for cluster udp session loadbalancer")
		argClusterSctpSessionLoadBalancer = pflag.String("cluster-sctp-session-loadbalancer", "cluster-sctp-session-lb", "The name for cluster sctp session loadbalancer")

		argWorkerNum       = pflag.Int("worker-num", 3, "The parallelism of each worker")
		argEnablePprof     = pflag.Bool("enable-pprof", false, "Enable pprof")
		argPprofPort       = pflag.Int("pprof-port", 10660, "The port to get profiling data")
		argNodePgProbeTime = pflag.Int("nodepg-probe-time", 1, "The probe interval for node port-group, the unit is minute")

		argNetworkType             = pflag.String("network-type", consts.NetworkTypeGeneve, "The ovn network type")
		argDefaultProviderName     = pflag.String("default-provider-name", "provider", "The vlan or vxlan type default provider interface name")
		argDefaultInterfaceName    = pflag.String("default-interface-name", "", "The default host interface name in the vlan/vxlan type")
		argDefaultExchangeLinkName = pflag.Bool("default-exchange-link-name", false, "exchange link names of OVS bridge and the provider nic in the default provider-network")
		argDefaultVlanName         = pflag.String("default-vlan-name", "kubeovn-vlan", "The default vlan name")
		argDefaultVlanID           = pflag.Int("default-vlan-id", 1, "The default vlan id")
		argLsDnatModDlDst          = pflag.Bool("ls-dnat-mod-dl-dst", true, "Set ethernet destination address for DNAT on logical switch")
		argLsCtSkipDstLportIPs     = pflag.Bool("ls-ct-skip-dst-lport-ips", true, "Skip conntrack for direct traffic between lports")
		argPodNicType              = pflag.String("pod-nic-type", "veth-pair", "The default pod network nic implementation type")
		argEnableLb                = pflag.Bool("enable-lb", true, "Enable load balancer")
		argEnableNP                = pflag.Bool("enable-np", true, "Enable network policy support")
		argEnableEipSnat           = pflag.Bool("enable-eip-snat", true, "Enable EIP and SNAT")
		argEnableExternalVpc       = pflag.Bool("enable-external-vpc", true, "Enable external vpc support")
		argEnableEcmp              = pflag.Bool("enable-ecmp", false, "Enable ecmp route for centralized subnet")
		argKeepVMIP                = pflag.Bool("keep-vm-ip", true, "Whether to keep ip for kubevirt pod when pod is rebuild")
		argEnableLbSvc             = pflag.Bool("enable-lb-svc", false, "Whether to support loadbalancer service")
		argEnableMetrics           = pflag.Bool("enable-metrics", true, "Whether to support metrics query")

		argExternalGatewayConfigNS = pflag.String("external-gateway-config-ns", "kube-system", "The namespace of configmap external-gateway-config, default: kube-system")
		argExternalGatewaySwitch   = pflag.String("external-gateway-switch", "external", "The name of the external gateway switch which is a ovs bridge to provide external network, default: external")
		argExternalGatewayNet      = pflag.String("external-gateway-net", "external", "The name of the external network which mappings with an ovs bridge, default: external")
		argExternalGatewayVlanID   = pflag.Int("external-gateway-vlanid", 0, "The vlanId of port ln-ovn-external, default: 0")
		argNodeLocalDNSIP          = pflag.String("node-local-dns-ip", "", "The node local dns ip , this feature is using the local dns cache in k8s")

		argGCInterval      = pflag.Int("gc-interval", 360, "The interval between GC processes, default 360 seconds")
		argInspectInterval = pflag.Int("inspect-interval", 20, "The interval between inspect processes, default 20 seconds")

		argBfdMinTx      = pflag.Int("bfd-min-tx", 100, "This is the minimum interval, in milliseconds, ovn would like to use when transmitting BFD Control packets")
		argBfdMinRx      = pflag.Int("bfd-min-rx", 100, "This is the minimum interval, in milliseconds, between received BFD Control packets")
		argBfdDetectMult = pflag.Int("detect-mult", 3, "The negotiated transmit interval, multiplied by this value, provides the Detection Time for the receiving system in Asynchronous mode.")

		argLogFile        = pflag.String("log-file", "/var/log/kube-ovn/kube-ovn-controller.log", "Filename is the file to write logs to. Backup log files will be retained in the same directory")
		argLogFileMaxSize = pflag.Int("log-file-max-size", 100, "MaxSize is the maximum size in megabytes of the log file before it gets rotated. It defaults to 100 megabytes.")
		argLogFileMaxAge  = pflag.Int("log-file-max-age", 30, "MaxAge is the maximum number of days to retain old log files based on the timestamp encoded in their filename.")

		argMetricsAddr          = pflag.String("metrics-bind-address", "0", "The address the metric endpoint binds to. Use the port :8080. If not set, it will be 0 in order to disable the metrics server")
		argProbeAddr            = pflag.String("health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
		argEnableLeaderElection = pflag.Bool("leader-elect", false, "Enable leader election for controller manager. Enabling this will ensure there is only one active controller manager.")
		argSecureMetrics        = pflag.Bool("metrics-secure", false, "If set the metrics endpoint is served securely")
		argEnableHTTP2          = pflag.Bool("enable-http2", false, "If set, HTTP/2 will be enabled for the metrics and webhook servers")
	)

	opts := zap.Options{
		Development: true,
	}
	opts.BindFlags(flag.CommandLine)

	pflag.CommandLine.AddGoFlagSet(flag.CommandLine)
	pflag.Parse()

	config := &Configuration{
		OvnNbAddr:                       *argOvnNbAddr,
		OvnSbAddr:                       *argOvnSbAddr,
		OvnTimeout:                      *argOvnTimeout,
		CustCrdRetryMinDelay:            *argCustCrdRetryMinDelay,
		CustCrdRetryMaxDelay:            *argCustCrdRetryMaxDelay,
		KubeConfigFile:                  *argKubeConfigFile,
		DefaultSubnet:                   *argDefaultSubnet,
		DefaultSubnetCIDR:               *argDefaultSubnetCIDR,
		DefaultSubnetGateway:            *argDefaultSubnetGateway,
		DefaultSubnetGatewayCheck:       *argDefaultSubnetGatewayCheck,
		DefaultSubnetLogicalGateway:     *argDefaultSubnetLogicalGateway,
		DefaultSubnetU2OInterconnection: *argDefaultSubnetU2OInterconnection,
		DefaultSubnetExcludeIps:         *argDefaultSubnetExcludeIps,
		DefaultVpc:                      *argDefaultVpc,
		JoinSubnet:                      *argJoinSubnet,
		JoinSubnetCIDR:                  *argJoinSubnetCIDR,
		JoinSubnetGateway:               *argJoinSubnetGateway,
		ServiceClusterIPRange:           *argServiceClusterIPRange,
		ClusterTCPLoadBalancer:          *argClusterTCPLoadBalancer,
		ClusterUDPLoadBalancer:          *argClusterUDPLoadBalancer,
		ClusterSctpLoadBalancer:         *argClusterSctpLoadBalancer,
		ClusterTCPSessionLoadBalancer:   *argClusterTCPSessionLoadBalancer,
		ClusterUDPSessionLoadBalancer:   *argClusterUDPSessionLoadBalancer,
		ClusterSctpSessionLoadBalancer:  *argClusterSctpSessionLoadBalancer,
		WorkerNum:                       *argWorkerNum,
		EnablePprof:                     *argEnablePprof,
		PprofPort:                       *argPprofPort,
		NetworkType:                     *argNetworkType,
		DefaultVlanID:                   *argDefaultVlanID,
		LsDnatModDlDst:                  *argLsDnatModDlDst,
		LsCtSkipDstLportIPs:             *argLsCtSkipDstLportIPs,
		DefaultProviderName:             *argDefaultProviderName,
		DefaultHostInterface:            *argDefaultInterfaceName,
		DefaultExchangeLinkName:         *argDefaultExchangeLinkName,
		DefaultVlanName:                 *argDefaultVlanName,
		PodName:                         os.Getenv("POD_NAME"),
		PodNamespace:                    os.Getenv("KUBE_NAMESPACE"),
		PodNicType:                      *argPodNicType,
		EnableLb:                        *argEnableLb,
		EnableNP:                        *argEnableNP,
		EnableEipSnat:                   *argEnableEipSnat,
		EnableExternalVpc:               *argEnableExternalVpc,
		ExternalGatewayConfigNS:         *argExternalGatewayConfigNS,
		ExternalGatewaySwitch:           *argExternalGatewaySwitch,
		ExternalGatewayNet:              *argExternalGatewayNet,
		ExternalGatewayVlanID:           *argExternalGatewayVlanID,
		EnableEcmp:                      *argEnableEcmp,
		EnableKeepVMIP:                  *argKeepVMIP,
		NodePgProbeTime:                 *argNodePgProbeTime,
		GCInterval:                      *argGCInterval,
		InspectInterval:                 *argInspectInterval,
		EnableLbSvc:                     *argEnableLbSvc,
		EnableMetrics:                   *argEnableMetrics,
		BfdMinTx:                        *argBfdMinTx,
		BfdMinRx:                        *argBfdMinRx,
		BfdDetectMult:                   *argBfdDetectMult,
		NodeLocalDNSIP:                  *argNodeLocalDNSIP,
		LogFile:                         *argLogFile,
		LogFileMaxsize:                  *argLogFileMaxSize,
		LogFileMaxAge:                   *argLogFileMaxAge,

		MetricsAddr:          *argMetricsAddr,
		SecureMetrics:        *argSecureMetrics,
		EnableLeaderElection: *argEnableLeaderElection,
		ProbeAddr:            *argProbeAddr,
		EnableHTTP2:          *argEnableHTTP2,
	}

	opts.EncoderConfigOptions = []zap.EncoderConfigOption{
		func(cfg *zapcore.EncoderConfig) {
			cfg.EncodeTime = zapcore.RFC3339TimeEncoder
		},
	}
	opts.ZapOpts = []gozap.Option{
		gozap.AddCaller(),
	}
	if !opts.Development {
		opts.DestWriter = zapcore.AddSync(&lumberjack.Logger{
			Filename: config.LogFile,
			MaxSize:  config.LogFileMaxsize,
			MaxAge:   config.LogFileMaxAge,
			Compress: true,
		})
	}
	log.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	if config.NetworkType == consts.NetworkTypeVlan && config.DefaultHostInterface == "" {
		return nil, fmt.Errorf("no host nic for vlan")
	}

	if config.DefaultSubnetGateway == "" {
		gw, err := utils.GetGwByCidr(config.DefaultSubnetCIDR)
		if err != nil {
			startupLogger.Error(err, "failed to get default subnet gateway by cidr", "cidr", config.DefaultSubnetCIDR)
			return nil, err
		}
		config.DefaultSubnetGateway = gw
	}

	if config.DefaultSubnetExcludeIps == "" {
		config.DefaultSubnetExcludeIps = config.DefaultSubnetGateway
	}

	if config.JoinSubnetGateway == "" {
		gw, err := utils.GetGwByCidr(config.JoinSubnetCIDR)
		if err != nil {
			startupLogger.Error(err, "failed to get join subnet gateway by cidr", "cidr", config.JoinSubnetCIDR)
			return nil, err
		}
		config.JoinSubnetGateway = gw
	}

	{
		var cfg *rest.Config
		var err error
		if config.KubeConfigFile == "" {
			startupLogger.Info("no --kubeconfig, use in-cluster kubernetes config")
			cfg, err = rest.InClusterConfig()
		} else {
			cfg, err = clientcmd.BuildConfigFromFlags("", config.KubeConfigFile)
		}
		if err != nil {
			startupLogger.Error(err, "failed to build kubeconfig")
			return nil, err
		}
		cfg.QPS = 100
		cfg.Burst = 200
		cfg.ContentType = "application/json"
		cfg.AcceptContentTypes = "application/vnd.kubernetes.protobuf,application/json"
		config.KubeRestConfig = cfg
	}

	if err := utils.CheckSystemCIDR([]string{config.JoinSubnetCIDR, config.DefaultSubnetCIDR, config.ServiceClusterIPRange}); err != nil {
		return nil, fmt.Errorf("check system cidr failed, %v", err)
	}

	startupLogger.Info("succeed to parse flags", "config", config)
	return config, nil
}
