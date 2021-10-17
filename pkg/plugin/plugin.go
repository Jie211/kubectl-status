package plugin

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"io/ioutil"
	"strings"
	"text/template"
	_ "unsafe" // required for using go:linkname in the file

	"github.com/pkg/errors"
	sfs "github.com/rakyll/statik/fs"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/cli-runtime/pkg/resource"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/kubectl/pkg/cmd/get"
	kyaml "sigs.k8s.io/yaml"

	_ "github.com/bergerx/kubectl-status/pkg/plugin/statik" // generated by statik
)

//go:linkname signame runtime.signame
func signame(sig uint32) string

func NewResourceStatusQuery(
	clientGetter *genericclioptions.ConfigFlags,
	namespace string,
	allNamespaces bool,
	enforceNamespace bool,
	filenames []string,
	selector string,
	fieldSelector string,
	args []string,
) *ResourceStatusQuery {
	return &ResourceStatusQuery{
		clientGetter,
		namespace,
		allNamespaces,
		enforceNamespace,
		filenames,
		selector,
		fieldSelector,
		args,
	}
}

type ResourceStatusQuery struct {
	clientGetter     *genericclioptions.ConfigFlags
	namespace        string
	allNamespaces    bool
	enforceNamespace bool
	filenames        []string
	selector         string
	fieldSelector    string
	args             []string
}

func (q ResourceStatusQuery) resolveResourceInfos(resourceResult *resource.Result) ([]*resource.Info, error) {
	err := resourceResult.Err()
	if err != nil {
		return nil, errors.WithMessage(err, "Failed during querying of resources")
	}
	resourceInfos, err := resourceResult.Infos()
	if err != nil {
		return nil, errors.WithMessage(err, "Failed getting resource  infos")
	}
	return resourceInfos, nil
}

func (q ResourceStatusQuery) getQueriedResources() ([]*resource.Info, error) {
	resourceInfos, err := q.resolveResourceInfos(resource.
		NewBuilder(q.clientGetter).
		Unstructured().
		NamespaceParam(q.namespace).DefaultNamespace().AllNamespaces(q.allNamespaces).
		FilenameParam(q.enforceNamespace, &resource.FilenameOptions{Filenames: q.filenames}).
		LabelSelectorParam(q.selector).
		FieldSelectorParam(q.fieldSelector).
		ResourceTypeOrNameArgs(true, q.args...).
		ContinueOnError().
		Latest().
		Flatten().
		Do())
	if err != nil {
		return nil, errors.WithMessage(err, "Failed getting resource infos")
	}
	if len(resourceInfos) == 0 {
		if !q.allNamespaces && q.namespace != "" {
			fmt.Printf("No resources found in %s namespace\n", q.namespace)
		} else {
			fmt.Printf("No resources found.\n")
		}
	}
	return resourceInfos, nil
}

func (q ResourceStatusQuery) PrintRenderedResourceInfos(resourceInfos []*resource.Info) []error {
	var allRenderErrs []error
	for _, resourceInfo := range resourceInfos {
		err := q.PrintRenderedResource(resourceInfo)
		if err != nil {
			allRenderErrs = append(allRenderErrs, err)
		}
	}
	return allRenderErrs
}

func (q ResourceStatusQuery) PrintRenderedQueriedResources() []error {
	resourceInfos, err := q.getQueriedResources()
	if err != nil {
		return []error{err}
	}
	return q.PrintRenderedResourceInfos(resourceInfos)
}

func (q ResourceStatusQuery) RenderOtherResources(namespace, kind, name string) []error {
	resourceInfos, err := q.resolveResourceInfos(resource.
		NewBuilder(q.clientGetter).
		Unstructured().
		NamespaceParam(namespace).
		ResourceTypeOrNameArgs(true, kind, name).
		ContinueOnError().
		Latest().
		Flatten().
		Do())
	if err != nil {
		return []error{err}
	}
	return q.PrintRenderedResourceInfos(resourceInfos)
}

func (q ResourceStatusQuery) GetKubeGetFunc() func(string, ...string) []interface{} {
	return func(namespace string, args ...string) []interface{} {
		resourceInfos, _ := q.resolveResourceInfos(q.getResourceQueryResults(namespace, args))

		var out []interface{}
		for _, resourceInfo := range resourceInfos {
			unstructuredObj, _ := runtime.DefaultUnstructuredConverter.ToUnstructured(resourceInfo.Object)
			out = append(out, unstructuredObj)
		}
		return out
	}
}

func (q ResourceStatusQuery) GetKubeGetByLabelsMapFunc() func(string, string, map[string]interface{}) []interface{} {
	return func(namespace, kind string, labels map[string]interface{}) []interface{} {
		var labelPairs []string
		for k, v := range labels {
			labelPairs = append(labelPairs, fmt.Sprintf("%s=%s", k, v))
		}
		selector := strings.Join(labelPairs, ",")
		resourceResult := resource.
			NewBuilder(q.clientGetter).
			Unstructured().
			NamespaceParam(q.namespace).
			LabelSelectorParam(selector).
			ContinueOnError().
			Latest().
			Flatten().
			Do()
		resourceInfos, _ := q.resolveResourceInfos(resourceResult)
		var out []interface{}
		for _, resourceInfo := range resourceInfos {
			obj := resourceInfo.Object
			unstructuredObj, _ := runtime.DefaultUnstructuredConverter.ToUnstructured(obj)
			out = append(out, unstructuredObj)
		}
		return out
	}
}

func (q ResourceStatusQuery) GetKubeGetServicesMatchingPod() func(map[string]interface{}) []interface{} {
	return func(podMap map[string]interface{}) []interface{} {
		var pod v1.Pod
		_ = runtime.DefaultUnstructuredConverter.FromUnstructured(podMap, &pod)
		restConfig, _ := q.clientGetter.ToRESTConfig()
		clientSet, _ := kubernetes.NewForConfig(restConfig)
		svcs, _ := clientSet.CoreV1().Services(q.namespace).List(context.TODO(), metav1.ListOptions{})
		var out []interface{}
		for _, svc := range svcs.Items {
			if svc.Spec.Type == "ExternalName" {
				continue
			}
			if isSubset(svc.Spec.Selector, pod.Labels) {
				// TODO: its likely that this serialisation method is not the right one
				svc.SetGroupVersionKind(schema.GroupVersionKind{Version: "v1", Kind: "Service"})
				svcUnstructured, _ := runtime.DefaultUnstructuredConverter.ToUnstructured(&svc)
				out = append(out, svcUnstructured)
			}
		}
		return out
	}
}

// Checks if a is subset of b
func isSubset(a, b map[string]string) bool {
	for k, v := range a {
		if v != b[k] {
			return false
		}
	}
	return true
}

func (q ResourceStatusQuery) GetKubeGetFirstFunc() func(string, ...string) interface{} {
	return func(namespace string, args ...string) interface{} {
		resourceInfos, _ := q.resolveResourceInfos(q.getResourceQueryResults(namespace, args))
		if len(resourceInfos) < 1 {
			return ""
		}
		unstructuredObj, _ := runtime.DefaultUnstructuredConverter.ToUnstructured(resourceInfos[0].Object)
		return unstructuredObj
	}
}

func (q ResourceStatusQuery) GetIncludeObjFunc() func(string, ...string) string {
	return func(namespace string, args ...string) string {
		if namespace == "" && len(args) == 0 {
			return ""
		}
		resourceInfos, err := q.resolveResourceInfos(q.getResourceQueryResults(namespace, args))
		if err != nil {
			return ""
		}
		var runtimeObjList []runtime.Object
		for _, resourceInfo := range resourceInfos {
			runtimeObjList = append(runtimeObjList, resourceInfo.Object)
		}
		get.NewRuntimeSorter(runtimeObjList, ".metadata.creationTimestamp").Sort()

		output := ""
		for _, resourceInfo := range runtimeObjList {
			renderOutput, _ := q.RenderResource(resourceInfo)
			output = fmt.Sprintf("%s\n%s", output, renderOutput)
		}
		return output
	}
}

func (q ResourceStatusQuery) GetIncludeOwnersFunc() func(map[string]interface{}) string {
	return func(obj map[string]interface{}) string {
		//objR := obj.(runtime.Object)
		unstructuredObj := unstructured.Unstructured{Object: obj}
		owners := unstructuredObj.GetOwnerReferences()
		owner := owners[0] // TODO: We should pick the one with controller, but using first item addresses most cases if not all.
		includeObjFunc := q.GetIncludeObjFunc()
		return includeObjFunc(unstructuredObj.GetNamespace(), owner.Kind, owner.Name)
	}
}

func (q ResourceStatusQuery) getGetEventsFunc() func(map[string]interface{}) map[string]interface{} {
	return func(obj map[string]interface{}) map[string]interface{} {
		unstructuredObj := unstructured.Unstructured{Object: obj}
		restConfig, _ := q.clientGetter.ToRESTConfig()
		clientSet, _ := kubernetes.NewForConfig(restConfig)
		runtimeObj := unstructuredObj.DeepCopyObject()
		events, _ := clientSet.CoreV1().Events(unstructuredObj.GetNamespace()).Search(scheme.Scheme, runtimeObj)
		unstructuredEvents, _ := runtime.DefaultUnstructuredConverter.ToUnstructured(&events)
		return unstructuredEvents
	}
}

func (q ResourceStatusQuery) getResourceQueryResults(namespace string, args []string) *resource.Result {
	return resource.
		NewBuilder(q.clientGetter).
		Unstructured().
		NamespaceParam(namespace).
		ResourceTypeOrNameArgs(true, args...).
		ContinueOnError().
		Flatten().
		Do()
}

func (q ResourceStatusQuery) PrintRenderedResource(resourceInfo *resource.Info) error {
	renderOutput, err := q.RenderResource(resourceInfo.Object)
	// Add a newline at the beginning of every template for readability
	// Add a newline at the end of every template, as they don't end with a newline
	fmt.Printf("\n%s\n", renderOutput)
	return err
}

func (q ResourceStatusQuery) RenderResource(obj runtime.Object) (string, error) {
	out, err := runtime.DefaultUnstructuredConverter.ToUnstructured(obj)
	if err != nil {
		return "", errors.WithMessage(err, "Failed getting unstructured object")
	}
	restConfig, err := q.clientGetter.ToRESTConfig()
	if err != nil {
		return "", errors.WithMessage(err, "Failed getting rest config")
	}
	kindInjectFuncMap := map[string][]func(obj runtime.Object, restConfig *rest.Config, out map[string]interface{}) error{
		"Node":        {includePodDetailsOnNode, includeNodeStatsSummary},
		"Pod":         {includePodMetrics}, // kubectl get --raw /api/v1/nodes/minikube/proxy/stats/summary --> .pods[] | select podRef | containers[] | select name
		"StatefulSet": {includeStatefulSetDiff},
		"Ingress":     {includeIngressServices},
	}
	kind := obj.GetObjectKind().GroupVersionKind().Kind
	functions := kindInjectFuncMap[kind]
	for _, f := range functions {
		err = f(obj, restConfig, out)
		if err != nil {
			return "", err
		}
	}

	var output bytes.Buffer
	err = renderTemplateForMap(&output, out, &q)
	renderOutput := output.String()
	return renderOutput, err
}

func RenderFile(manifestFilename string) (string, error) {
	var out map[string]interface{}
	manifestFile, _ := ioutil.ReadFile(manifestFilename)
	err := kyaml.Unmarshal(manifestFile, &out)
	if err != nil {
		return "", errors.WithMessage(err, "Failed getting JSON for object")
	}
	var output bytes.Buffer
	err = renderTemplateForMap(&output, out)
	if err != nil {
		return "", err
	}
	return output.String(), nil
}

func renderTemplateForMap(wr io.Writer, v map[string]interface{}, queries ...*ResourceStatusQuery) error {
	if len(queries) > 0 {
		// If a ResourceStatusQuery is passed than use it, if not than its likely a test run with a local file.
		query := queries[0]
		funcMap["kubeGet"] = query.GetKubeGetFunc()
		funcMap["kubeGetByLabelsMap"] = query.GetKubeGetByLabelsMapFunc()
		funcMap["kubeGetServicesMatchingPod"] = query.GetKubeGetServicesMatchingPod()
		funcMap["kubeGetFirst"] = query.GetKubeGetFirstFunc()
		funcMap["includeObj"] = query.GetIncludeObjFunc()
		funcMap["includeOwners"] = query.GetIncludeOwnersFunc()
		funcMap["getEvents"] = query.getGetEventsFunc()
	}
	tmpl, err := getParsedTemplates()
	if err != nil {
		return err
	}
	objKind := v["kind"].(string)
	kindTemplateName := findTemplateName(tmpl, objKind)
	return tmpl.ExecuteTemplate(wr, kindTemplateName, v)
}

func findTemplateName(tmpl *template.Template, kind string) string {
	// Returns the kind name if such template exists in templates, else returnDefaultResource
	var kindTemplateName string
	if t := tmpl.Lookup(kind); t != nil {
		kindTemplateName = kind
	} else {
		kindTemplateName = "DefaultResource"
	}
	return kindTemplateName
}

func getParsedTemplates() (*template.Template, error) {
	templateText, err := getTemplate()
	if err != nil {
		return nil, err
	}
	funcMap := getFuncMap()
	tmpl, err := template.
		New("templates.tmpl").
		Funcs(funcMap).
		Parse(templateText)
	if err != nil {
		return nil, err
	}
	funcMap["include"] = include
	tmpl.Funcs(funcMap)
	return tmpl, nil
}

func getTemplate() (string, error) {
	statikFS, err := sfs.New()
	if err != nil {
		return "", errors.WithMessage(err, "Failed initiating statikFS")
	}

	// Access individual files by their paths.
	templatesFile := "/templates.tmpl"
	t, err := statikFS.Open(templatesFile)
	if err != nil {
		return "", errors.WithMessage(err, "Failed opening template from statikFS")
	}
	defer t.Close()

	contents, err := ioutil.ReadAll(t)
	if err != nil {
		return "", errors.WithMessage(err, "Failed reading template from statikFS")
	}
	return string(contents), nil
}

// obj is usually a runtime.Object (resourceInfo.Object)
func objInterfaceToObjMap(obj interface{}) (map[string]interface{}, error) {
	return runtime.DefaultUnstructuredConverter.ToUnstructured(obj)
}

func objMapToUnstructured(obj map[string]interface{}) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: obj}
}

// example out is "out := &v1.StatefulSet{}"
// in also supports runtime.Unstructured types
func objInterfaceToSpecificObject(in interface{}, out interface{}) error {
	return scheme.Scheme.Convert(in, out, nil)
}
