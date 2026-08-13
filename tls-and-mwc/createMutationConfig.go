package main

import (
	"bytes"
	"context"
	"log"
	"os"

	v1 "k8s.io/api/admissionregistration/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	ctrl "sigs.k8s.io/controller-runtime"
)

func CreateMutationConfig(ctx context.Context, caPEM *bytes.Buffer) {

	var (
		webhookNamespace  = os.Getenv("WEBHOOK_NAMESPACE")
		mutationCfgName   = os.Getenv("MUTATE_CONFIG")
		webhookService    = os.Getenv("WEBHOOK_SERVICE")
		namespaceToMutate = os.Getenv("NAMESPACE_TO_MUTATE")
	)
	config := ctrl.GetConfigOrDie()
	kubeClient, err := kubernetes.NewForConfig(config)
	if err != nil {
		panic("failed to set go -client")
	}

	path := "/mutate"
	// fail := v1.Fail
	fail := v1.Ignore
	port := int32(8443)

	log.Println("WEBHOOK_NAMESPACE: ", webhookNamespace)
	log.Println("MUTATE_CONFIG: ", mutationCfgName)
	log.Println("WEBHOOK_SERVICE: ", webhookService)
	log.Println("NAMESPACE_TO_MUTATE: ", namespaceToMutate)

	if namespaceToMutate == "" {
		panic("NAMESPACE_TO_MUTATE must be set")
	}

	mutateconfig := &v1.MutatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{
			Name: mutationCfgName,
		},
		Webhooks: []v1.MutatingWebhook{{
			Name: webhookService + "." + webhookNamespace + ".svc",
			ClientConfig: v1.WebhookClientConfig{
				CABundle: caPEM.Bytes(), // CA bundle created in generateTLSCerts command
				Service: &v1.ServiceReference{
					Name:      webhookService,
					Namespace: webhookNamespace,
					Path:      &path,
					Port:      &port,
				},
			},
			Rules: []v1.RuleWithOperations{
				{
					Operations: []v1.OperationType{
						v1.Create, v1.Update,
					},
					Rule: v1.Rule{
						APIGroups:   []string{"apps"},
						APIVersions: []string{"v1"},
						Resources:   []string{"deployments"},
					},
				},
			},
			NamespaceSelector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"enable-" + mutationCfgName: "true",
				},
				MatchExpressions: []metav1.LabelSelectorRequirement{
					{
						Key:      "kubernetes.io/metadata.name",
						Operator: metav1.LabelSelectorOpIn,
						Values:   []string{namespaceToMutate},
					},
				},
			},
			AdmissionReviewVersions: []string{"v1"},
			FailurePolicy:           &fail,
			SideEffects: func() *v1.SideEffectClass {
				sideEffect := v1.SideEffectClassNone
				return &sideEffect
			}(),
		}},
	}

	webhookConfigs := kubeClient.AdmissionregistrationV1().MutatingWebhookConfigurations()
	// Try to create configuration when it when missing, update it when it
	// exists, and retry the lookup if another process wins a create race.
	existingConfig, err := webhookConfigs.Get(ctx, mutationCfgName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		if _, err := webhookConfigs.Create(ctx, mutateconfig, metav1.CreateOptions{}); err == nil {
			return
		} else if !apierrors.IsAlreadyExists(err) {
			panic(err)
		}

		// Another process created the configuration after the Get call. Fetch it
		// and continue with the update path.
		existingConfig, err = webhookConfigs.Get(ctx, mutationCfgName, metav1.GetOptions{})
	}
	if err != nil {
		panic(err)
	}

	// Preserve metadata managed by Kubernetes or other tooling, while replacing
	// the webhook specification managed by this program.
	existingConfig.Webhooks = mutateconfig.Webhooks
	if _, err := webhookConfigs.Update(ctx, existingConfig, metav1.UpdateOptions{}); err != nil {
		panic(err)
	}
}
