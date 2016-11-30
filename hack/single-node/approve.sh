ssh_ident="$(vagrant ssh-config | awk '/IdentityFile/ {print $2}' | tr -d '"')"
ssh_port="$(vagrant ssh-config | awk '/Port [0-9]+/ {print $2}')"

CSR=$(kubectl --kubeconfig=kubeconfig get CertificateSigningRequests -o jsonpath='{.items[0].metadata.name}')
echo ${CSR}
ssh -q -o stricthostkeychecking=no -i ${ssh_ident} -p ${ssh_port} core@127.0.0.1 "git clone https://github.com/gtank/csrctl.git && ./csrctl/csrctl.sh approve ${CSR}"
