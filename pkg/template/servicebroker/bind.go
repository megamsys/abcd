package servicebroker

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
	"k8s.io/apiserver/pkg/authentication/user"
	kapi "k8s.io/kubernetes/pkg/api"
	"k8s.io/kubernetes/pkg/apis/authorization"

	"github.com/golang/glog"
	"github.com/openshift/origin/pkg/authorization/util"
	"github.com/openshift/origin/pkg/openservicebroker/api"
	templateapi "github.com/openshift/origin/pkg/template/api"
)

// copied from vendor/k8s.io/kubernetes/pkg/kubelet/envvars/envvars.go
// TODO: when the API for returning information from the bind call is cleaned up
// and we're no longer temporarily using the environment variable style, remove
// this.
func makeEnvVariableName(str string) string {
	return strings.ToUpper(strings.Replace(str, "-", "_", -1))
}

// getServices returns environment variable style details for all services
// created by a given template in its namespace.  This API may not currently be
// considered stable.  TODO: if a template creates a service in another
// namespace, its details will not currently be returned.
func (b *Broker) getServices(u user.Info, namespace, instanceID string) (map[string]string, *api.Response) {
	glog.V(4).Infof("Template service broker: getServices")

	requirement, _ := labels.NewRequirement(templateapi.TemplateInstanceLabel, selection.Equals, []string{instanceID})

	if err := util.Authorize(b.kc.Authorization().SubjectAccessReviews(), u, &authorization.ResourceAttributes{
		Namespace: namespace,
		Verb:      "list",
		Group:     kapi.GroupName,
		Resource:  "services",
	}); err != nil {
		return nil, api.Forbidden(err)
	}

	serviceList, err := b.kc.Core().Services(namespace).List(metav1.ListOptions{LabelSelector: labels.NewSelector().Add(*requirement).String()})
	if err != nil {
		if kerrors.IsForbidden(err) {
			return nil, api.Forbidden(err)
		}

		return nil, api.InternalServerError(err)
	}

	services := map[string]string{}
	for _, service := range serviceList.Items {
		if !kapi.IsServiceIPSet(&service) || len(service.Spec.Ports) == 0 {
			continue
		}

		name := makeEnvVariableName(service.Name) + "_SERVICE_HOST"
		services[name] = service.Spec.ClusterIP

		name = makeEnvVariableName(service.Name) + "_SERVICE_PORT"
		services[name] = strconv.Itoa(int(service.Spec.Ports[0].Port))

		for _, port := range service.Spec.Ports {
			if port.Name != "" {
				services[name+"_"+makeEnvVariableName(port.Name)] = strconv.Itoa(int(port.Port))
			}
		}
	}

	return services, nil
}

// getSecrets returns usernames and passwords contained in all BasicAuth secrets
// created by a given template in its namespace.  This API may not currently be
// considered stable.  TODO: if a template creates a secret in another
// namespace, its details will not currently be returned.
func (b *Broker) getSecrets(u user.Info, namespace, instanceID string) (map[string]string, *api.Response) {
	glog.V(4).Infof("Template service broker: getSecrets")

	requirement, _ := labels.NewRequirement(templateapi.TemplateInstanceLabel, selection.Equals, []string{instanceID})

	if err := util.Authorize(b.kc.Authorization().SubjectAccessReviews(), u, &authorization.ResourceAttributes{
		Namespace: namespace,
		Verb:      "list",
		Group:     kapi.GroupName,
		Resource:  "secrets",
	}); err != nil {
		return nil, api.Forbidden(err)
	}

	secretList, err := b.kc.Core().Secrets(namespace).List(metav1.ListOptions{LabelSelector: labels.NewSelector().Add(*requirement).String()})
	if err != nil {
		if kerrors.IsForbidden(err) {
			return nil, api.Forbidden(err)
		}

		return nil, api.InternalServerError(err)
	}

	secrets := map[string]string{}
	for _, secret := range secretList.Items {
		if secret.Type != kapi.SecretTypeBasicAuth {
			continue
		}

		name := makeEnvVariableName(secret.Name + "_" + kapi.BasicAuthUsernameKey)
		secrets[name] = string(secret.Data[kapi.BasicAuthUsernameKey])

		name = makeEnvVariableName(secret.Name + "_" + kapi.BasicAuthPasswordKey)
		secrets[name] = string(secret.Data[kapi.BasicAuthPasswordKey])
	}

	return secrets, nil
}

// Bind returns the secrets and services from a provisioned template.
func (b *Broker) Bind(instanceID, bindingID string, breq *api.BindRequest) *api.Response {
	glog.V(4).Infof("Template service broker: Bind: instanceID %s, bindingID %s", instanceID, bindingID)

	if errs := ValidateBindRequest(breq); len(errs) > 0 {
		return api.BadRequest(errs.ToAggregate())
	}

	if len(breq.Parameters) != 1 {
		return api.BadRequest(errors.New("parameters not supported on bind"))
	}

	impersonate := breq.Parameters[templateapi.RequesterUsernameParameterKey]
	u := &user.DefaultInfo{Name: impersonate}

	brokerTemplateInstance, err := b.templateclient.BrokerTemplateInstances().Get(instanceID, metav1.GetOptions{})
	if err != nil {
		if kerrors.IsNotFound(err) {
			return api.BadRequest(err)
		}

		return api.InternalServerError(err)
	}

	namespace := brokerTemplateInstance.Spec.TemplateInstance.Namespace

	// since we can, cross-check breq.ServiceID and
	// templateInstance.Spec.Template.UID.

	if err := util.Authorize(b.kc.Authorization().SubjectAccessReviews(), u, &authorization.ResourceAttributes{
		Namespace: namespace,
		Verb:      "get",
		Group:     templateapi.GroupName,
		Resource:  "templateinstances",
	}); err != nil {
		return api.Forbidden(err)
	}

	templateInstance, err := b.templateclient.TemplateInstances(namespace).Get(brokerTemplateInstance.Spec.TemplateInstance.Name, metav1.GetOptions{})
	if err != nil {
		return api.InternalServerError(err)
	}
	if breq.ServiceID != string(templateInstance.Spec.Template.UID) {
		return api.BadRequest(errors.New("service_id does not match provisioned service"))
	}

	services, resp := b.getServices(u, namespace, instanceID)
	if resp != nil {
		return resp
	}

	secrets, resp := b.getSecrets(u, namespace, instanceID)
	if resp != nil {
		return resp
	}

	// TODO: this API may not currently be considered stable.
	credentials := map[string]interface{}{}
	credentials["services"] = services
	credentials["secrets"] = secrets

	// The OSB API requires this function to be idempotent (restartable).  If
	// any actual change was made, per the spec, StatusCreated is returned, else
	// StatusOK.

	status := http.StatusCreated
	for _, id := range brokerTemplateInstance.Spec.BindingIDs {
		if id == bindingID {
			status = http.StatusOK
			break
		}
	}
	if status == http.StatusCreated { // binding not found; create it
		brokerTemplateInstance.Spec.BindingIDs = append(brokerTemplateInstance.Spec.BindingIDs, bindingID)
		brokerTemplateInstance, err = b.templateclient.BrokerTemplateInstances().Update(brokerTemplateInstance)
		if err != nil {
			return api.InternalServerError(err)
		}
	}

	return api.NewResponse(status, &api.BindResponse{Credentials: credentials}, nil)
}
