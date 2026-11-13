// Copyright 2026 Microsoft Corporation
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package verifiers

import (
	"context"
	"fmt"
	"time"

	"github.com/onsi/ginkgo/v2"

	authenticationv1 "k8s.io/api/authentication/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"

	configv1 "github.com/openshift/api/config/v1"
	imagev1 "github.com/openshift/api/image/v1"
	configv1client "github.com/openshift/client-go/config/clientset/versioned/typed/config/v1"
	imagev1client "github.com/openshift/client-go/image/clientset/versioned/typed/image/v1"
)

const (
	internalRegistryHostDefault  = "image-registry.openshift-image-registry.svc:5000"
	imageRegistryPushPullBuilder = "builder"
	imageStreamNameDefault       = "push-pull-test"
	imageStreamTagDefault        = "e2e"
	sourceImageDefault           = "registry.k8s.io/pause:3.9"

	// imageRegistryOperatorTimeout bounds how long to wait for the "image-registry"
	// ClusterOperator to become Available. The registry Deployment schedules onto
	// worker nodes, so this can only succeed once a node pool is ready.
	imageRegistryOperatorTimeout = 10 * time.Minute
	// imageRegistryPushJobTimeout bounds how long to wait for the skopeo push Job to
	// complete successfully.
	imageRegistryPushJobTimeout = 5 * time.Minute
	// imageRegistryPullTimeout bounds how long to wait for the pull test pod to report
	// a successfully pulled image.
	imageRegistryPullTimeout = 3 * time.Minute
)

type verifyImageRegistryOperator struct{}

func (v verifyImageRegistryOperator) Name() string {
	return "VerifyImageRegistryOperator"
}

// VerifyImageRegistryOperator exercises the image-registry operator identity's
// data-plane permissions against the Azure Blob Storage backing the internal registry:
// wait for the image-registry ClusterOperator, push a public image with skopeo using the
// namespace builder service account, confirm the ImageStreamTag exists, then pull the
// image in a pod and verify the kubelet reported a successful pull.
func VerifyImageRegistryOperator() HostedClusterVerifier {
	return verifyImageRegistryOperator{}
}

func (v verifyImageRegistryOperator) Verify(ctx context.Context, adminRESTConfig *rest.Config) error {
	if err := waitForClusterOperatorAvailable(ctx, adminRESTConfig, "image-registry", imageRegistryOperatorTimeout); err != nil {
		return fmt.Errorf("image-registry cluster operator did not become available: %w", err)
	}

	kubeClient, err := kubernetes.NewForConfig(adminRESTConfig)
	if err != nil {
		return fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	imageClient, err := imagev1client.NewForConfig(adminRESTConfig)
	if err != nil {
		return fmt.Errorf("failed to create image.openshift.io client: %w", err)
	}

	ns, err := kubeClient.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{GenerateName: "image-registry-push-pull-"},
	}, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("failed to create test namespace: %w", err)
	}
	testNamespace := ns.Name
	defer func() {
		_ = kubeClient.CoreV1().Namespaces().Delete(context.WithoutCancel(ctx), testNamespace, metav1.DeleteOptions{})
	}()

	// The namespace's "builder" service account only has the "system:image-builder" role,
	// which can update an existing ImageStream but cannot auto-vivify one on push.
	_, err = imageClient.ImageStreams(testNamespace).Create(ctx, &imagev1.ImageStream{
		ObjectMeta: metav1.ObjectMeta{Name: imageStreamNameDefault},
	}, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("failed to create ImageStream %s/%s: %w", testNamespace, imageStreamNameDefault, err)
	}

	err = pollUntilReady(ctx, v.Name()+"/builder-sa", 2*time.Minute, 5*time.Second, adminRESTConfig, DefaultDiagnoseTimeout, nil, func(ctx context.Context) error {
		_, err := kubeClient.CoreV1().ServiceAccounts(testNamespace).Get(ctx, imageRegistryPushPullBuilder, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("builder service account not yet provisioned in namespace %s: %w", testNamespace, err)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("builder service account was never created in namespace %s: %w", testNamespace, err)
	}

	tokenResp, err := kubeClient.CoreV1().ServiceAccounts(testNamespace).CreateToken(ctx, imageRegistryPushPullBuilder, &authenticationv1.TokenRequest{
		Spec: authenticationv1.TokenRequestSpec{
			ExpirationSeconds: to.Ptr(int64(imageRegistryPushJobTimeout.Seconds()) + 600),
		},
	}, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("failed to mint a token for service account %s/%s: %w", testNamespace, imageRegistryPushPullBuilder, err)
	}

	pushTokenSecret, err := kubeClient.CoreV1().Secrets(testNamespace).Create(ctx, &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "registry-push-token"},
		StringData: map[string]string{"token": tokenResp.Status.Token},
	}, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("failed to store the builder token in a secret: %w", err)
	}

	destinationImageRef := fmt.Sprintf("docker://%s/%s/%s:%s", internalRegistryHostDefault, testNamespace, imageStreamNameDefault, imageStreamTagDefault)
	pushJobName := "registry-push-test"
	pushJob := registryPushJob(testNamespace, pushJobName, pushTokenSecret.Name, destinationImageRef)
	_, err = kubeClient.BatchV1().Jobs(testNamespace).Create(ctx, pushJob, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("failed to create registry push job: %w", err)
	}

	if err := waitForRegistryPushJobSuccess(ctx, kubeClient, testNamespace, pushJobName, imageRegistryPushJobTimeout); err != nil {
		return fmt.Errorf("failed to push image into the internal registry — the registry's Azure Blob Storage data plane identity may lack write permission: %w", err)
	}

	imageStreamTagName := fmt.Sprintf("%s:%s", imageStreamNameDefault, imageStreamTagDefault)
	err = pollUntilReady(ctx, v.Name()+"/imagestreamtag", 2*time.Minute, 10*time.Second, adminRESTConfig, DefaultDiagnoseTimeout, nil, func(ctx context.Context) error {
		_, err := imageClient.ImageStreamTags(testNamespace).Get(ctx, imageStreamTagName, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("ImageStreamTag %s/%s not yet visible: %w", testNamespace, imageStreamTagName, err)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("expected ImageStreamTag %s/%s to exist after a successful push: %w", testNamespace, imageStreamTagName, err)
	}

	pulledImageRef := fmt.Sprintf("%s/%s/%s:%s", internalRegistryHostDefault, testNamespace, imageStreamNameDefault, imageStreamTagDefault)
	_, err = kubeClient.CoreV1().Pods(testNamespace).Create(ctx, registryPullTestPod(pulledImageRef), metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("failed to create pod that pulls from the internal registry: %w", err)
	}

	if err := VerifyImagePulled(testNamespace, internalRegistryHostDefault, imageStreamNameDefault, imageRegistryPullTimeout).Verify(ctx, adminRESTConfig); err != nil {
		return fmt.Errorf("failed to pull %s back out of the internal registry — the registry's Azure Blob Storage data plane identity may lack read permission: %w", pulledImageRef, err)
	}

	return nil
}

func registryPushJob(namespace, jobName, pushTokenSecretName, destinationImageRef string) *batchv1.Job {
	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{Name: jobName, Namespace: namespace},
		Spec: batchv1.JobSpec{
			BackoffLimit: to.Ptr(int32(0)),
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					RestartPolicy: corev1.RestartPolicyNever,
					Volumes: []corev1.Volume{
						{
							Name:         "scratch",
							VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}},
						},
					},
					Containers: []corev1.Container{
						{
							Name:  "skopeo-push",
							Image: "quay.io/skopeo/stable:latest",
							Command: []string{
								"skopeo", "copy",
								"--dest-creds", "serviceaccount:$(REGISTRY_TOKEN)",
								"--dest-tls-verify=false",
								"docker://" + sourceImageDefault,
								destinationImageRef,
							},
							Env: []corev1.EnvVar{
								{Name: "HOME", Value: "/scratch"},
								{
									Name: "REGISTRY_TOKEN",
									ValueFrom: &corev1.EnvVarSource{
										SecretKeyRef: &corev1.SecretKeySelector{
											LocalObjectReference: corev1.LocalObjectReference{Name: pushTokenSecretName},
											Key:                  "token",
										},
									},
								},
							},
							VolumeMounts: []corev1.VolumeMount{
								{Name: "scratch", MountPath: "/scratch"},
							},
							SecurityContext: restrictedSecurityContext(),
						},
					},
				},
			},
		},
	}
}

func registryPullTestPod(imageRef string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "registry-pull-test"},
		Spec: corev1.PodSpec{
			RestartPolicy: corev1.RestartPolicyNever,
			Containers: []corev1.Container{
				{
					Name:            "pull-test",
					Image:           imageRef,
					ImagePullPolicy: corev1.PullAlways,
					SecurityContext: restrictedSecurityContext(),
				},
			},
		},
	}
}

func restrictedSecurityContext() *corev1.SecurityContext {
	return &corev1.SecurityContext{
		RunAsNonRoot:             to.Ptr(true),
		AllowPrivilegeEscalation: to.Ptr(false),
		SeccompProfile: &corev1.SeccompProfile{
			Type: corev1.SeccompProfileTypeRuntimeDefault,
		},
		Capabilities: &corev1.Capabilities{
			Drop: []corev1.Capability{"ALL"},
		},
	}
}

// waitForClusterOperatorAvailable polls the named ClusterOperator until its Available
// condition is True. This intentionally checks a single operator rather than using
// VerifyAllClusterOperatorsAvailable: operators unrelated to the image registry can take
// longer than this flow needs to wait for.
func waitForClusterOperatorAvailable(ctx context.Context, adminRESTConfig *rest.Config, operatorName string, timeout time.Duration) error {
	configClient, err := configv1client.NewForConfig(adminRESTConfig)
	if err != nil {
		return fmt.Errorf("failed to create config client: %w", err)
	}

	var lastErr error
	pollErr := wait.PollUntilContextTimeout(ctx, 10*time.Second, timeout, true, func(ctx context.Context) (bool, error) {
		co, err := configClient.ClusterOperators().Get(ctx, operatorName, metav1.GetOptions{})
		if err != nil {
			lastErr = fmt.Errorf("failed to get ClusterOperator %s: %w", operatorName, err)
			return false, nil
		}
		for _, cond := range co.Status.Conditions {
			if cond.Type != configv1.OperatorAvailable {
				continue
			}
			if cond.Status == configv1.ConditionTrue {
				return true, nil
			}
			lastErr = fmt.Errorf("ClusterOperator %s not available: %s=%s (%s)", operatorName, cond.Type, cond.Status, cond.Message)
			return false, nil
		}
		lastErr = fmt.Errorf("ClusterOperator %s has no Available condition", operatorName)
		return false, nil
	})
	if pollErr == nil {
		return nil
	}
	if lastErr != nil {
		return fmt.Errorf("ClusterOperator %s did not become available within %s: %w", operatorName, timeout, lastErr)
	}
	return fmt.Errorf("ClusterOperator %s did not become available within %s: %w", operatorName, timeout, pollErr)
}

func waitForRegistryPushJobSuccess(ctx context.Context, kubeClient kubernetes.Interface, namespace, jobName string, timeout time.Duration) error {
	var previousDiagnostic string
	pollErr := wait.PollUntilContextTimeout(ctx, 10*time.Second, timeout, true, func(ctx context.Context) (bool, error) {
		job, err := kubeClient.BatchV1().Jobs(namespace).Get(ctx, jobName, metav1.GetOptions{})
		if err != nil {
			return false, fmt.Errorf("failed to get job %s/%s: %w", namespace, jobName, err)
		}
		if job.Status.Succeeded > 0 {
			return true, nil
		}
		if job.Status.Failed > 0 {
			diagnostic := fetchPodLogsForJob(ctx, kubeClient, namespace, jobName)
			if diagnostic != previousDiagnostic {
				ginkgo.GinkgoWriter.Printf("registry push job %s/%s failed; pod logs:\n%s\n", namespace, jobName, diagnostic)
				previousDiagnostic = diagnostic
			}
			return false, fmt.Errorf("registry push job %s/%s failed:\n%s", namespace, jobName, diagnostic)
		}
		return false, nil
	})
	if pollErr != nil {
		return fmt.Errorf("registry push job %s/%s did not succeed within %s: %w", namespace, jobName, timeout, pollErr)
	}
	return nil
}

func fetchPodLogsForJob(ctx context.Context, kubeClient kubernetes.Interface, namespace, jobName string) string {
	pods, err := kubeClient.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("job-name=%s", jobName),
	})
	if err != nil {
		return fmt.Sprintf("<failed to list pods for job %s: %v>", jobName, err)
	}
	if len(pods.Items) == 0 {
		return fmt.Sprintf("<no pods found for job %s>", jobName)
	}

	podName := pods.Items[0].Name
	logs, err := kubeClient.CoreV1().Pods(namespace).GetLogs(podName, &corev1.PodLogOptions{}).Do(ctx).Raw()
	if err != nil {
		return fmt.Sprintf("<failed to fetch logs for pod %s: %v>", podName, err)
	}
	return string(logs)
}
