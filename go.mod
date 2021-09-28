module github.com/bergerx/kubectl-status

go 1.12

replace github.com/gorilla/rpc v1.2.0+incompatible => github.com/gorilla/rpc v1.2.0 // https://github.com/gorilla/rpc/issues/65#issuecomment-518834577

require (
	github.com/Masterminds/sprig/v3 v3.2.2
	github.com/dustin/go-humanize v1.0.0
	github.com/fatih/color v1.13.0
	github.com/pkg/errors v0.9.1
	github.com/pmezard/go-difflib v1.0.0
	github.com/rakyll/statik v0.1.7
	github.com/spf13/cast v1.4.1
	github.com/spf13/cobra v1.2.1
	github.com/spf13/viper v1.9.0
	k8s.io/api v0.22.2
	k8s.io/apimachinery v0.22.2
	k8s.io/cli-runtime v0.22.2
	k8s.io/client-go v0.22.2
	k8s.io/kubectl v0.22.2
	k8s.io/metrics v0.22.2
	sigs.k8s.io/yaml v1.3.0
)
