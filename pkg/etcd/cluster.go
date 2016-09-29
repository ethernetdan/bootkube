package etcd

import (
	"fmt"
	"path/filepath"
	"time"

	"golang.org/x/net/context"
)

var (
	// MemberNamePattern is the format string used to name etcd cluster members.
	MemberNamePattern = "%s-%04d"
)

// NewCluster creates an etcd cluster with the number of members given in size (must be 1 or greater). Members are named
// by appending a number to namePrefix. Each member is given directory under rootDataDir named after itself. The hostname
// used in URLs is defined by host. Each member will be assigned a peer port and a client port from the range specified
// in beginPeer & endPeer and beginClient & endClient.
func NewCluster(size int, namePrefix, rootDataDir, clusterToken, host string, beginPeer, beginClient int) (*Cluster, error) {
	if size < 1 {
		return nil, fmt.Errorf("cluster must have at least one member: given size %d", size)
	}

	// populate peerMap
	curPeer := beginPeer
	peerMap := map[string][]string{}
	for num := 0; num < size; num++ {
		name := memberName(namePrefix, num)
		peerURL := createEtcdURL("http", host, curPeer)
		peerMap[name] = []string{peerURL}
		curPeer++
	}

	cluster := &Cluster{
		servers: map[string]*Server{},
		peerMap: peerMap,
	}
	curClient := beginClient
	for num := 0; num < size; num++ {
		name := memberName(namePrefix, num)
		dataDir := filepath.Join(rootDataDir, name)
		clientURL := createEtcdURL("http", host, curClient)
		peerURLs := peerMap[name]
		server, err := NewServer(name, dataDir, clusterToken, true, []string{clientURL}, peerURLs, peerMap)
		if err != nil {
			return nil, fmt.Errorf("failed to configure server '%s': %v", name, err)
		}
		cluster.clientURLs = append(cluster.clientURLs, clientURL)
		curClient++
		cluster.servers[name] = server
	}
	return cluster, nil
}

// Cluster is an Etcd cluster that can be started and stopped.
type Cluster struct {
	clientURLs []string
	servers    map[string]*Server
	peerMap    map[string][]string
}

// Start will start each member of the cluster.
func (c *Cluster) Start() (err error) {
	for _, server := range c.servers {
		if err = server.Start(); err != nil {
			// if fails, stop already running members
			c.Stop()
			return fmt.Errorf("failed to start '%s': %v", server.Name(), err)
		}
	}
	return nil
}

// Stop will attempt to stop each member in the cluster.
func (c *Cluster) Stop() {
	for _, server := range c.servers {
		server.Stop()
	}
}

// TurnDown will remove each etcd in the cluster as a member, sleeping the given time between each.
func (c *Cluster) TurnDown(t time.Duration) {
	for _, s := range c.servers {
		id := s.server.ID()
		s.server.RemoveMember(context.Background(), uint64(id))
		time.Sleep(t)
		s.Stop()
	}
}

// ClientURLs returns the client URLs provided at cluster creation.
func (c *Cluster) ClientURLs() []string {
	return c.clientURLs
}

// ClusterString returns a string with peers names and peer URLs provided at creation in a format usable with --initial-cluster
func (c *Cluster) ClusterString() (cluster string) {
	first := true
	for name, urls := range c.peerMap {
		if !first {
			cluster += ","
		}
		first = false
		for _, url := range urls {
			cluster += fmt.Sprintf("%s=%s", name, url)
		}
	}
	return
}

// createEtcdURL returns a URL formatted for use with etcd
func createEtcdURL(protocol, host string, port int) string {
	return fmt.Sprintf("%s://%s:%d", protocol, host, port)
}

// memberName returns an etcd clusters name using its member number and prefix
func memberName(prefix string, num int) string {
	return fmt.Sprintf(MemberNamePattern, prefix, num)
}
