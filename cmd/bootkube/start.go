package main

import (
	"errors"
	"flag"
	"fmt"
	"io/ioutil"

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
		assetDir string
		etcdHost string
	}
)

func init() {
	cmdRoot.AddCommand(cmdStart)
	cmdStart.Flags().StringVar(&startOpts.etcdHost, "etcd-host", "127.0.0.1", "host etcd runs on")
	cmdStart.Flags().StringVar(&startOpts.assetDir, "asset-dir", "", "Path to the cluster asset directory. Expected layout genereted by the `bootkube render` command.")
}

func runCmdStart(cmd *cobra.Command, args []string) error {
	// potentially should be removed later
	dataDir, err := ioutil.TempDir("", "etcd-data")
	if err != nil {
		return fmt.Errorf("error creating temporary data directory for etcd: %v", err)
	}

	bk, err := bootkube.NewBootkube(bootkube.Config{
		AssetDir:    startOpts.assetDir,
		EtcdDataDir: dataDir,
		EtcdName:    "bootkube",
		EtcdHost:    startOpts.etcdHost,
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
	if startOpts.etcdHost == "" {
		return errors.New("missing required flag: --etcd-host")
	}
	if startOpts.assetDir == "" {
		return errors.New("missing required flag: --asset-dir")
	}
	return nil
}
