package main

import (
	"errors"
	"flag"
	"fmt"
	"io/ioutil"
	"net/url"

	"github.com/spf13/cobra"
	"k8s.io/kubernetes/pkg/util"

	"github.com/kubernetes-incubator/bootkube/pkg/bootkube"
)

var (
	cmdStart = &cobra.Command{
		Use:          "start",
		Short:        "Start the bootkube service",
		Long:         "",
		PreRunE:      validateStartOpts,
		RunE:         runCmdStart,
		SilenceUsage: true,
	}

	startOpts struct {
		assetDir   string
		etcdServer string
	}
)

func init() {
	cmdRoot.AddCommand(cmdStart)
	cmdStart.Flags().StringVar(&startOpts.etcdServer, "etcd-server", "http://127.0.0.1:4500", "Single etcd node to use during bootkube bootstrap process.")
	cmdStart.Flags().StringVar(&startOpts.assetDir, "asset-dir", "", "Path to the cluster asset directory. Expected layout genereted by the `bootkube render` command.")
}

func runCmdStart(cmd *cobra.Command, args []string) error {
	etcdServer, err := url.Parse(startOpts.etcdServer)
	if err != nil {
		return fmt.Errorf("Invalid etcd etcdServer %q: %v", startOpts.etcdServer, err)
	}

	// potentially should be removed later
	dataDir, err := ioutil.TempDir("", "etcd-data")
	if err != nil {
		return fmt.Errorf("error creating temporary data directory for etcd: %v", err)
	}

	etcdPeerURL, err := url.Parse("http://localhost:2382")
	if err != nil {
		return fmt.Errorf("error parsing etcd peer URL: %v", err)
	}

	bk, err := bootkube.NewBootkube(bootkube.Config{
		AssetDir:      startOpts.assetDir,
		EtcdClientURL: etcdServer,
		EtcdPeerURL:   etcdPeerURL,
		EtcdDataDir:   dataDir,
		EtcdName:      "bootkube",
	})

	if err != nil {
		return err
	}

	// set in util init() func, but lets not depend on that
	flag.Set("logtostderr", "true")
	util.InitLogs()
	defer util.FlushLogs()

	return bk.Run()
}

func validateStartOpts(cmd *cobra.Command, args []string) error {
	if startOpts.etcdServer == "" {
		return errors.New("missing required flag: --etcd-server")
	}
	if startOpts.assetDir == "" {
		return errors.New("missing required flag: --asset-dir")
	}
	return nil
}
