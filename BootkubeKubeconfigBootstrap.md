# Kubeconfig Bootstrapping

To perform the kubeconfig bootstrapping, checkout this branch and:

1. Compile modified bootkube:
```bash
make clean all
```

2. Move into single-node directory. Run all further commands from that directory.
```bash
cd hack/single-node
```

3. Run modified bootkube up
```bash
./bootkube-up
```

4. Wait for kubelet to request CSR. You can SSH onto the machine with `vagrant ssh` and check status with:
Both components are run with the highest verbosity.
**bootkube**
`tail -f /home/core/bootkube.log`

**kubelet**
`journalctl -f -u kubelet`

5. Approve Certificate
```bash
./approve.sh
```
