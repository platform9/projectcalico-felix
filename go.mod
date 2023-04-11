module github.com/projectcalico/felix

go 1.16

require (
	github.com/Microsoft/hcsshim v0.8.10-0.20200715222032-5eafd1556990
	github.com/aws/aws-sdk-go v1.35.24
	github.com/containernetworking/plugins v0.8.2
	github.com/davecgh/go-spew v1.1.1
	github.com/docopt/docopt-go v0.0.0-20180111231733-ee0de3bc6815
	github.com/go-ini/ini v1.44.0
	github.com/gogo/protobuf v1.3.2
	github.com/golang-collections/collections v0.0.0-20130729185459-604e922904d3
	github.com/golang-jwt/jwt/v4 v4.5.0 // indirect
	github.com/golang/protobuf v1.4.3
	github.com/google/gopacket v1.1.17
	github.com/google/netstack v0.0.0-20191123085552-55fcc16cd0eb
	github.com/ishidawataru/sctp v0.0.0-20191218070446-00ab2ac2db07
	github.com/kardianos/osext v0.0.0-20190222173326-2bc1f35cddc0
	github.com/kelseyhightower/envconfig v0.0.0-20180517194557-dd1402a4d99d
	github.com/kr/text v0.2.0 // indirect
	github.com/libp2p/go-reuseport v0.0.1
	github.com/mipearson/rfw v0.0.0-20170619235010-6f0a6f3266ba
	github.com/mitchellh/go-homedir v1.1.0
	github.com/niemeyer/pretty v0.0.0-20200227124842-a10e7caefd8e // indirect
	github.com/onsi/ginkgo v1.14.1
	github.com/onsi/gomega v1.10.1
	github.com/pkg/errors v0.9.1
	github.com/projectcalico/libcalico-go v1.7.2-0.20211119233600-e3f7c620522a
	github.com/projectcalico/pod2daemon v0.0.0-20210303175725-63bffe4262de
	github.com/projectcalico/typha v0.7.3-0.20211122094324-72659b47ead5
	github.com/prometheus/client_golang v1.11.1
	github.com/satori/go.uuid v1.2.0
	github.com/sirupsen/logrus v1.6.0
	github.com/spf13/cobra v1.1.1
	github.com/spf13/viper v1.7.0
	github.com/vishvananda/netlink v1.1.0
	golang.org/x/net v0.0.0-20211112202133-69e39bad7dc2
	golang.org/x/sync v0.0.0-20201207232520-09787c993a3a
	golang.org/x/sys v0.0.0-20210615035016-665e8c7367d1
	golang.zx2c4.com/wireguard/wgctrl v0.0.0-20200324154536-ceff61240acf
	google.golang.org/grpc v1.27.1
	gopkg.in/check.v1 v1.0.0-20200227125254-8fa46927fb4f // indirect
	k8s.io/api v0.19.6
	k8s.io/apimachinery v0.19.6
	k8s.io/client-go v0.19.6
	k8s.io/kubernetes v1.18.12
)

replace (
	github.com/Microsoft/hcsshim => github.com/projectcalico/hcsshim v0.8.9-calico
	github.com/sirupsen/logrus => github.com/projectcalico/logrus v0.0.0-20180701205716-fc9bbf2f5799

	// Need replacements for all the k8s subsidiary projects that are pulled in indirectly because
	// the kubernets repo pulls them in via a replacement to its own vendored copies, which doesn't work for
	// transient imports.
	k8s.io/api => k8s.io/api v0.18.12
	k8s.io/apiextensions-apiserver => k8s.io/apiextensions-apiserver v0.18.12
	k8s.io/apimachinery => k8s.io/apimachinery v0.18.12
	k8s.io/apiserver => k8s.io/apiserver v0.18.12
	k8s.io/cli-runtime => k8s.io/cli-runtime v0.18.12
	k8s.io/client-go => k8s.io/client-go v0.18.12
	k8s.io/cloud-provider => k8s.io/cloud-provider v0.18.12
	k8s.io/cluster-bootstrap => k8s.io/cluster-bootstrap v0.18.12
	k8s.io/code-generator => k8s.io/code-generator v0.18.12
	k8s.io/component-base => k8s.io/component-base v0.18.12
	k8s.io/component-helpers => k8s.io/component-helpers v0.18.12
	k8s.io/controller-manager => k8s.io/controller-manager v0.18.12
	k8s.io/cri-api => k8s.io/cri-api v0.18.12
	k8s.io/csi-translation-lib => k8s.io/csi-translation-lib v0.18.12
	k8s.io/kube-aggregator => k8s.io/kube-aggregator v0.18.12
	k8s.io/kube-controller-manager => k8s.io/kube-controller-manager v0.18.12
	k8s.io/kube-proxy => k8s.io/kube-proxy v0.18.12
	k8s.io/kube-scheduler => k8s.io/kube-scheduler v0.18.12
	k8s.io/kubectl => k8s.io/kubectl v0.18.12
	k8s.io/kubelet => k8s.io/kubelet v0.18.12
	k8s.io/legacy-cloud-providers => k8s.io/legacy-cloud-providers v0.18.12
	k8s.io/metrics => k8s.io/metrics v0.18.12
	k8s.io/mount-utils => k8s.io/mount-utils v0.18.12
	k8s.io/sample-apiserver => k8s.io/sample-apiserver v0.18.12
)

replace github.com/gogo/protobuf => github.com/gogo/protobuf v1.3.2

replace github.com/prometheus/client_golang => github.com/prometheus/client_golang v1.11.1

replace golang.org/x/crypto => golang.org/x/crypto v0.0.0-20220314234659-1baeb1ce4c0b

replace github.com/dgrijalva/jwt-go => github.com/golang-jwt/jwt/v4 v4.4.2
