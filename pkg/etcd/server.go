package etcd

import (
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"time"

	etcd "github.com/coreos/etcd/etcdserver"
	etcdhttp "github.com/coreos/etcd/etcdserver/api/v2http"
	etcdtransport "github.com/coreos/etcd/pkg/transport"
	"github.com/coreos/etcd/pkg/types"
)

// NewServer configures an Etcd instance that can later be started. In cluster, keys are names of members and
// values of that member's peer URLs. This member must be included in cluster.
func NewServer(name, dataDir, clusterToken string, newCluster bool, clientURLs, peerURLs []string, cluster map[string][]string) (*Server, error) {
	cURLs, err := types.NewURLs(clientURLs)
	if err != nil {
		return nil, fmt.Errorf("error parsing client URLs: %v", err)
	}
	pURLs, err := types.NewURLs(peerURLs)
	if err != nil {
		return nil, fmt.Errorf("error parsing peer URLs: %v", err)
	}

	// encode cluster with etcd types
	peerMap := map[string]types.URLs{}
	for name, v := range cluster {
		urls, err := types.NewURLs(v)
		if err != nil {
			return nil, fmt.Errorf("error parsing peer URLs for cluster member '%s': %v", name, err)
		}
		peerMap[name] = urls
	}

	config := &etcd.ServerConfig{
		Name:                name,
		ClientURLs:          cURLs,
		PeerURLs:            pURLs,
		DataDir:             dataDir,
		InitialPeerURLsMap:  peerMap,
		InitialClusterToken: clusterToken,

		NewCluster: newCluster,

		SnapCount:     etcd.DefaultSnapCount,
		MaxSnapFiles:  5,
		MaxWALFiles:   5,
		TickMs:        100,
		ElectionTicks: 10,
	}

	// attempt to create etcd server with given configuration
	server, err := etcd.NewServer(config)
	if err != nil {
		return nil, fmt.Errorf("error configuring etcd server: %v", err)
	}

	return &Server{
		config: config,
		server: server,
	}, nil
}

// Server is an Etcd server that is able to be started and stopped.
type Server struct {
	config *etcd.ServerConfig
	server *etcd.EtcdServer
	pLis   []net.Listener
	cLis   []net.Listener
}

// Start creates client & peer listeners, executes the etcd goroutine, and sets up client & peer HTTP handlers.
func (e *Server) Start() (err error) {
	// create listeners
	if e.pLis, err = createListeners(e.config.PeerURLs); err != nil {
		return fmt.Errorf("failed to create peer listeners: %v", err)
	}
	if e.cLis, err = createListeners(e.config.ClientURLs); err != nil {
		return fmt.Errorf("failed to create client listeners: %v", err)
	}

	// start etcd goroutine
	e.server.Start()

	// setup peer handlers
	ph := etcdhttp.NewPeerHandler(e.server)
	for _, l := range e.pLis {
		go func(l net.Listener) {
			srv := &http.Server{
				Handler: ph,
			}
			panic(srv.Serve(l))
		}(l)
	}

	// setup client handlers
	timeout := etcdRequestTimeout(e.config.ElectionTicks, e.config.TickMs)
	ch := etcdhttp.NewClientHandler(e.server, timeout)
	for _, l := range e.cLis {
		go func(l net.Listener) {
			srv := &http.Server{
				Handler:     ch,
				ReadTimeout: 5 * time.Minute,
			}
			panic(srv.Serve(l))
		}(l)
	}
	return nil
}

// Stop signals the etcd goroutine to exit and closes peer & client listeners.
func (e *Server) Stop() {
	if e.server != nil {
		e.server.Stop()
	}

	for _, l := range e.cLis {
		l.Close()
	}
	for _, l := range e.pLis {
		l.Close()
	}
}

// Name returns the name of the etcd member.
func (e *Server) Name() string {
	return e.config.Name
}

// createListeners creates a TCP listener with the provided URLs.
func createListeners(urls types.URLs) (listeners []net.Listener, err error) {
	for _, url := range urls {
		l, err := net.Listen("tcp", url.Host)
		if err != nil {
			return nil, err
		}

		l, err = etcdtransport.NewKeepAliveListener(l, url.Scheme, &tls.Config{})
		if err != nil {
			return nil, err
		}

		listeners = append(listeners, l)
	}
	return listeners, nil
}

// etcdRequestTimeout calculates the correct timeout using a method from from github.com/coreos/etcd/etcdserver/config.go
func etcdRequestTimeout(electionTicks int, tickMs uint) time.Duration {
	return 5*time.Second + 2*time.Duration(electionTicks)*time.Duration(tickMs)*time.Millisecond
}
