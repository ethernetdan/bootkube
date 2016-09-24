package bootkube

import (
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"time"

	etcd "github.com/coreos/etcd/etcdserver"
	etcdhttp "github.com/coreos/etcd/etcdserver/api/v2http"
	etcdtransport "github.com/coreos/etcd/pkg/transport"
	etcdtypes "github.com/coreos/etcd/pkg/types"
	"github.com/spf13/pflag"
	apiapp "k8s.io/kubernetes/cmd/kube-apiserver/app"
	apiserver "k8s.io/kubernetes/cmd/kube-apiserver/app/options"
	cmapp "k8s.io/kubernetes/cmd/kube-controller-manager/app"
	controller "k8s.io/kubernetes/cmd/kube-controller-manager/app/options"
	schedapp "k8s.io/kubernetes/plugin/cmd/kube-scheduler/app"
	scheduler "k8s.io/kubernetes/plugin/cmd/kube-scheduler/app/options"

	"github.com/kubernetes-incubator/bootkube/pkg/asset"
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
)

var requiredPods = []string{
	"kube-api-checkpoint",
	"kubelet",
	"kube-apiserver",
	"kube-scheduler",
	"kube-controller-manager",
}

type Config struct {
	AssetDir      string
	EtcdDataDir   string
	EtcdName      string
	EtcdClientURL *url.URL
	EtcdPeerURL   *url.URL
}

type bootkube struct {
	etcd       *etcd.EtcdServer
	etcdConfig *etcd.ServerConfig
	assetDir   string
	apiServer  *apiserver.APIServer
	controller *controller.CMServer
	scheduler  *scheduler.SchedulerServer
}

func NewBootkube(config Config) (*bootkube, error) {
	etcdClientURLs, err := etcdtypes.NewURLs([]string{config.EtcdClientURL.String()})
	if err != nil {
		return nil, err
	}

	etcdPeerURLs, err := etcdtypes.NewURLs([]string{config.EtcdPeerURL.String()})
	if err != nil {
		return nil, err
	}

	peersMap := map[string]etcdtypes.URLs{
		config.EtcdName: etcdPeerURLs,
	}

	etcdConfig := &etcd.ServerConfig{
		Name:               config.EtcdName,
		ClientURLs:         etcdClientURLs,
		PeerURLs:           etcdPeerURLs,
		DataDir:            config.EtcdDataDir,
		InitialPeerURLsMap: peersMap,

		NewCluster: true,

		SnapCount:     etcd.DefaultSnapCount,
		MaxSnapFiles:  5,
		MaxWALFiles:   5,
		TickMs:        100,
		ElectionTicks: 10,
	}

	etcdServer, err := etcd.NewServer(etcdConfig)
	if err != nil {
		return nil, fmt.Errorf("etcd config error: %v", err)
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
		"--etcd-servers=http://127.0.0.1:4500", //+ config.EtcdClientURL.String(),
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
		etcdConfig: etcdConfig,
		etcd:       etcdServer,
		apiServer:  apiServer,
		controller: cmServer,
		scheduler:  schedServer,
		assetDir:   config.AssetDir,
	}, nil
}

func (b *bootkube) Run() error {
	UserOutput("Running temporary bootstrap control plane...\n")

	listeners := createListenersOrPanic(b.etcdConfig.ClientURLs)
	b.etcd.Start()

	// setup client listeners
	timeout := etcdRequestTimeout(b.etcdConfig.ElectionTicks, b.etcdConfig.TickMs)
	ch := etcdhttp.NewClientHandler(b.etcd, timeout)
	for _, l := range listeners {
		go func(l net.Listener) {
			srv := &http.Server{
				Handler:     ch,
				ReadTimeout: 5 * time.Minute,
			}
			panic(srv.Serve(l))
		}(l)
	}

	time.Sleep(2 * time.Second)

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

// createListenersOrPanic either creates a TCP listener with the provided URLs or panics.
func createListenersOrPanic(urls etcdtypes.URLs) (listeners []net.Listener) {
	for _, url := range urls {
		l, err := net.Listen("tcp", url.Host)
		if err != nil {
			panic(err)
		}

		l, err = etcdtransport.NewKeepAliveListener(l, url.Scheme, &tls.Config{})
		if err != nil {
			panic(err)
		}

		listeners = append(listeners, l)
	}
	return listeners
}

// etcdRequestTimeout calculates the correct timeout using a method from from github.com/coreos/etcd/etcdserver/config.go
func etcdRequestTimeout(electionTicks int, tickMs uint) time.Duration {
	return 5*time.Second + 2*time.Duration(electionTicks)*time.Duration(tickMs)*time.Millisecond
}
