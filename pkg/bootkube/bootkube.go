package bootkube

import (
	"fmt"
	"path/filepath"
	"time"

	"github.com/spf13/pflag"
	apiapp "k8s.io/kubernetes/cmd/kube-apiserver/app"
	apiserver "k8s.io/kubernetes/cmd/kube-apiserver/app/options"
	cmapp "k8s.io/kubernetes/cmd/kube-controller-manager/app"
	controller "k8s.io/kubernetes/cmd/kube-controller-manager/app/options"
	schedapp "k8s.io/kubernetes/plugin/cmd/kube-scheduler/app"
	scheduler "k8s.io/kubernetes/plugin/cmd/kube-scheduler/app/options"

	"github.com/kubernetes-incubator/bootkube/pkg/asset"
	"github.com/kubernetes-incubator/bootkube/pkg/etcd"
)

const (
	assetTimeout = 10 * time.Minute
	// NOTE: using 8081 as the port is a temporary hack when there is a single api-server.
	// The self-hosted apiserver will immediately die if it cannot bind to the insecure interface.
	// However, if it can successfully bind to insecure interface, it will continue to retry
	// failures on the the secure interface.
	// Staggering the insecure port allows us to launch a self-hosted api-server on the same machine
	// as the bootkube, and the self-hosted api-server will continually retry binding to secure interface
	// and doesn't end up in a race with bootkube for the insecure port. When bootkube dies, the self-hosted
	// api-server is using the correct standard ports (443/8080).
	insecureAPIAddr = "http://127.0.0.1:8081"

	// etcdMembers is the number of etcd servers in a cluster. This number must be >=2 or new members can't be added.
	etcdMembers = 2

	// etcdClusterToken is the token etcd uses to bootstrap
	etcdClusterToken = "bootkube"

	// etcdHost is the host that etcd members will bind to
	etcdHost = "127.0.0.1"
)

var requiredPods = []string{
	"kube-api-checkpoint",
	"kubelet",
	"kube-apiserver",
	"kube-scheduler",
	"kube-controller-manager",
	"etcd0",
}

type Config struct {
	AssetDir    string
	EtcdDataDir string
	EtcdName    string
	EtcdHost    string
}

type bootkube struct {
	etcd       *etcd.Cluster
	assetDir   string
	apiServer  *apiserver.APIServer
	controller *controller.CMServer
	scheduler  *scheduler.SchedulerServer
}

func NewBootkube(config Config) (*bootkube, error) {
	etcdCluster, err := etcd.NewCluster(etcdMembers, config.EtcdName, config.EtcdDataDir, etcdClusterToken, config.EtcdHost, 6000, 6040)
	if err != nil {
		return nil, fmt.Errorf("could not create cluster: %v", err)
	}

	clientURLs := ""
	for pos, url := range etcdCluster.ClientURLs() {
		if pos > 0 {
			clientURLs += ","
		}
		clientURLs += url
	}

	apiServer := apiserver.NewAPIServer()
	fs := pflag.NewFlagSet("apiserver", pflag.ExitOnError)
	apiServer.AddFlags(fs)
	fs.Parse([]string{
		"--bind-address=0.0.0.0",
		"--secure-port=443",
		"--insecure-port=8081", // NOTE: temp hack for single-apiserver
		"--allow-privileged=true",
		"--tls-private-key-file=" + filepath.Join(config.AssetDir, asset.AssetPathAPIServerKey),
		"--tls-cert-file=" + filepath.Join(config.AssetDir, asset.AssetPathAPIServerCert),
		"--client-ca-file=" + filepath.Join(config.AssetDir, asset.AssetPathCACert),
		"--etcd-servers=" + clientURLs,
		"--service-cluster-ip-range=10.3.0.0/24",
		"--service-account-key-file=" + filepath.Join(config.AssetDir, asset.AssetPathServiceAccountPubKey),
		"--admission-control=ServiceAccount",
		"--runtime-config=extensions/v1beta1/deployments=true,extensions/v1beta1/daemonsets=true",
	})

	cmServer := controller.NewCMServer()
	fs = pflag.NewFlagSet("controllermanager", pflag.ExitOnError)
	cmServer.AddFlags(fs)
	fs.Parse([]string{
		"--master=" + insecureAPIAddr,
		"--service-account-private-key-file=" + filepath.Join(config.AssetDir, asset.AssetPathServiceAccountPrivKey),
		"--root-ca-file=" + filepath.Join(config.AssetDir, asset.AssetPathCACert),
		"--leader-elect=true",
	})

	schedServer := scheduler.NewSchedulerServer()
	fs = pflag.NewFlagSet("scheduler", pflag.ExitOnError)
	schedServer.AddFlags(fs)
	fs.Parse([]string{
		"--master=" + insecureAPIAddr,
		"--leader-elect=true",
	})

	return &bootkube{
		etcd:       etcdCluster,
		apiServer:  apiServer,
		controller: cmServer,
		scheduler:  schedServer,
		assetDir:   config.AssetDir,
	}, nil
}

func (b *bootkube) Run() error {
	UserOutput("Starting etcd cluster...\n")
	if err := b.etcd.Start(); err != nil {
		return err
	}

	wait := 2 * time.Second
	UserOutput("Waiting %v for etcd cluster to bootstrap...\n", wait)
	time.Sleep(wait)

	UserOutput("Running temporary bootstrap control plane...\n")
	errch := make(chan error)
	go func() { errch <- apiapp.Run(b.apiServer) }()
	go func() { errch <- cmapp.Run(b.controller) }()
	go func() { errch <- schedapp.Run(b.scheduler) }()
	go func() {
		if err := CreateAssets(filepath.Join(b.assetDir, asset.AssetPathManifests), assetTimeout); err != nil {
			errch <- err
		}
	}()
	go func() { errch <- WaitUntilPodsRunning(requiredPods, assetTimeout) }()

	// If any of the bootkube services exit, it means it is unrecoverable and we should exit.
	err := <-errch
	if err != nil {
		UserOutput("Error: %v\n", err)
	}
	return err
}

// All bootkube printing to stdout should go through this fmt.Printf wrapper.
// The stdout of bootkube should convey information useful to a human sitting
// at a terminal watching their cluster bootstrap itself. Otherwise the message
// should go to stderr.
func UserOutput(format string, a ...interface{}) {
	fmt.Printf(format, a...)
}
