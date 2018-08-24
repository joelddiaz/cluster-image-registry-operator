package operator

import (
	"fmt"

	appsapi "github.com/openshift/api/apps/v1"
	authapi "github.com/openshift/api/authorization/v1"

	"github.com/openshift/cluster-image-registry-operator/pkg/apis/dockerregistry/v1alpha1"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

const (
	defaultName                = "registry"
	defaultPort                = 5000
	healthzRoute               = "/healthz"
	healthzRouteTimeoutSeconds = 5
	serviceAccountName         = "registry"

	checksumOperatorAnnotation    = "dockerregistry.operator.openshift.io/checksum"
	storageTypeOperatorAnnotation = "dockerregistry.operator.openshift.io/storagetype"
)

// addOwnerRefToObject appends the desired OwnerReference to the object
func addOwnerRefToObject(obj metav1.Object, ownerRef metav1.OwnerReference) {
	obj.SetOwnerReferences(append(obj.GetOwnerReferences(), ownerRef))
}

// asOwner returns an OwnerReference set as the memcached CR
func asOwner(cr *v1alpha1.OpenShiftDockerRegistry) metav1.OwnerReference {
	trueVar := true
	return metav1.OwnerReference{
		APIVersion: cr.APIVersion,
		Kind:       cr.Kind,
		Name:       cr.Name,
		UID:        cr.UID,
		Controller: &trueVar,
	}
}

func generateLivenessProbeConfig(port int, https bool) *corev1.Probe {
	probeConfig := generateProbeConfig(port, https)
	probeConfig.InitialDelaySeconds = 10

	return probeConfig
}

func generateReadinessProbeConfig(port int, https bool) *corev1.Probe {
	return generateProbeConfig(port, https)
}

func generateProbeConfig(port int, https bool) *corev1.Probe {
	var scheme corev1.URIScheme
	if https {
		scheme = corev1.URISchemeHTTPS
	}
	return &corev1.Probe{
		TimeoutSeconds: healthzRouteTimeoutSeconds,
		Handler: corev1.Handler{
			HTTPGet: &corev1.HTTPGetAction{
				Scheme: scheme,
				Path:   healthzRoute,
				Port:   intstr.FromInt(port),
			},
		},
	}
}

func generateSecurityContext(cr *v1alpha1.OpenShiftDockerRegistry) *corev1.PodSecurityContext {
	result := &corev1.PodSecurityContext{}
	/*
		if len(conf.SupplementalGroups) > 0 {
			result.SupplementalGroups = []int64{}
			for _, val := range conf.SupplementalGroups {
				// The errors are handled by Complete()
				if groupID, err := strconv.ParseInt(val, 10, 64); err == nil {
					result.SupplementalGroups = append(result.SupplementalGroups, groupID)
				}
			}
		}
		if len(conf.FSGroup) > 0 {
			if groupID, err := strconv.ParseInt(conf.FSGroup, 10, 64); err == nil {
				result.FSGroup = &groupID
			}
		}
	*/
	return result
}

func GenerateServiceAccount(cr *v1alpha1.OpenShiftDockerRegistry, dc *appsapi.DeploymentConfig) *corev1.ServiceAccount {
	sa := &corev1.ServiceAccount{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "ServiceAccount",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      serviceAccountName,
			Namespace: dc.Namespace,
		},
	}
	addOwnerRefToObject(sa, asOwner(cr))
	return sa
}

func GenerateClusterRoleBinding(cr *v1alpha1.OpenShiftDockerRegistry, dc *appsapi.DeploymentConfig) *authapi.ClusterRoleBinding {
	crb := &authapi.ClusterRoleBinding{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "rbac.authorization.k8s.io/v1",
			Kind:       "ClusterRoleBinding",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: fmt.Sprintf("registry-%s-role", defaultName),
		},
		Subjects: []corev1.ObjectReference{
			{
				Kind:      "ServiceAccount",
				Name:      serviceAccountName,
				Namespace: dc.Namespace,
			},
		},
		RoleRef: corev1.ObjectReference{
			Kind: "ClusterRole",
			Name: "system:registry",
		},
	}
	addOwnerRefToObject(crb, asOwner(cr))
	return crb
}

func GenerateService(cr *v1alpha1.OpenShiftDockerRegistry, dc *appsapi.DeploymentConfig) *corev1.Service {
	svc := &corev1.Service{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "Service",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      dc.Name,
			Namespace: dc.Namespace,
			Labels:    dc.Labels,
		},
		Spec: corev1.ServiceSpec{
			Selector: dc.Labels,
			Ports: []corev1.ServicePort{
				{
					Name:       fmt.Sprintf("%d-tcp", defaultPort),
					Port:       defaultPort,
					Protocol:   "TCP",
					TargetPort: intstr.FromInt(defaultPort),
				},
			},
		},
	}
	addOwnerRefToObject(svc, asOwner(cr))
	return svc
}

func GenerateDeploymentConfig(cr *v1alpha1.OpenShiftDockerRegistry) (*appsapi.DeploymentConfig, error) {
	storageType := ""
	tls := false
	label := map[string]string{
		"docker-registry": "default",
	}

	var (
		storageConfigured int
		env               []corev1.EnvVar
		mounts            []corev1.VolumeMount
		volumes           []corev1.Volume
	)

	env = append(env,
		corev1.EnvVar{Name: "REGISTRY_HTTP_ADDR", Value: fmt.Sprintf(":%d", defaultPort)},
		corev1.EnvVar{Name: "REGISTRY_HTTP_NET", Value: "tcp"},
		corev1.EnvVar{Name: "REGISTRY_STORAGE_CACHE_BLOBDESCRIPTOR", Value: "inmemory"},
		corev1.EnvVar{Name: "REGISTRY_STORAGE_DELETE_ENABLED", Value: "true"},
		corev1.EnvVar{Name: "REGISTRY_OPENSHIFT_QUOTA_ENABLED", Value: "true"},
	)

	if cr.Spec.Storage.Filesystem != nil {
		if cr.Spec.Storage.Filesystem.VolumeSource.HostPath != nil {
			return nil, fmt.Errorf("HostPath is not supported")
		}

		env = append(env,
			corev1.EnvVar{Name: "REGISTRY_STORAGE", Value: "filesystem"},
			corev1.EnvVar{Name: "REGISTRY_STORAGE_FILESYSTEM_ROOTDIRECTORY", Value: "/registry"},
		)

		vol := corev1.Volume{
			Name:         "registry-storage",
			VolumeSource: cr.Spec.Storage.Filesystem.VolumeSource,
		}

		volumes = append(volumes, vol)
		mounts = append(mounts, corev1.VolumeMount{Name: vol.Name, MountPath: "/registry"})

		storageConfigured += 1
	}

	if cr.Spec.Storage.Azure != nil {
		storageType = "azure"
		env = append(env,
			corev1.EnvVar{Name: "REGISTRY_STORAGE", Value: storageType},
			corev1.EnvVar{Name: "REGISTRY_STORAGE_AZURE_ACCOUNTNAME", Value: cr.Spec.Storage.Azure.AccountName},
			corev1.EnvVar{Name: "REGISTRY_STORAGE_AZURE_ACCOUNTKEY", Value: cr.Spec.Storage.Azure.AccountKey},
			corev1.EnvVar{Name: "REGISTRY_STORAGE_AZURE_CONTAINER", Value: cr.Spec.Storage.Azure.Container},
		)
		storageConfigured += 1
	}

	if cr.Spec.Storage.GCS != nil {
		storageType = "gcs"
		env = append(env,
			corev1.EnvVar{Name: "REGISTRY_STORAGE", Value: storageType},
			corev1.EnvVar{Name: "REGISTRY_STORAGE_GCS_BUCKET", Value: cr.Spec.Storage.GCS.Bucket},
		)
		storageConfigured += 1
	}

	if cr.Spec.Storage.S3 != nil {
		storageType = "s3"
		env = append(env,
			corev1.EnvVar{Name: "REGISTRY_STORAGE", Value: storageType},
			corev1.EnvVar{Name: "REGISTRY_STORAGE_S3_ACCESSKEY", Value: cr.Spec.Storage.S3.AccessKey},
			corev1.EnvVar{Name: "REGISTRY_STORAGE_S3_SECRETKEY", Value: cr.Spec.Storage.S3.SecretKey},
			corev1.EnvVar{Name: "REGISTRY_STORAGE_S3_BUCKET", Value: cr.Spec.Storage.S3.Bucket},
			corev1.EnvVar{Name: "REGISTRY_STORAGE_S3_REGION", Value: cr.Spec.Storage.S3.Region},
			corev1.EnvVar{Name: "REGISTRY_STORAGE_S3_REGIONENDPOINT", Value: cr.Spec.Storage.S3.RegionEndpoint},
			corev1.EnvVar{Name: "REGISTRY_STORAGE_S3_ENCRYPT", Value: fmt.Sprintf("%v", cr.Spec.Storage.S3.Encrypt)},
		)
		storageConfigured += 1
	}

	if cr.Spec.Storage.Swift != nil {
		storageType = "swift"
		env = append(env,
			corev1.EnvVar{Name: "REGISTRY_STORAGE", Value: storageType},
			corev1.EnvVar{Name: "REGISTRY_STORAGE_SWIFT_AUTHURL", Value: cr.Spec.Storage.Swift.AuthURL},
			corev1.EnvVar{Name: "REGISTRY_STORAGE_SWIFT_USERNAME", Value: cr.Spec.Storage.Swift.Username},
			corev1.EnvVar{Name: "REGISTRY_STORAGE_SWIFT_PASSWORD", Value: cr.Spec.Storage.Swift.Password},
			corev1.EnvVar{Name: "REGISTRY_STORAGE_SWIFT_CONTAINER", Value: cr.Spec.Storage.Swift.Container},
		)
		storageConfigured += 1
	}

	if storageConfigured != 1 {
		return nil, fmt.Errorf("it is not possible to initialize more than one storage backend at the same time")
	}

	dc := &appsapi.DeploymentConfig{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "apps.openshift.io/v1",
			Kind:       "DeploymentConfig",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "docker-registry",
			Namespace: cr.Namespace,
			Labels:    label,
			Annotations: map[string]string{
				storageTypeOperatorAnnotation: storageType,
			},
		},
		Spec: appsapi.DeploymentConfigSpec{
			Replicas: cr.Spec.Replicas,
			Selector: label,
			Triggers: []appsapi.DeploymentTriggerPolicy{
				{
					Type: appsapi.DeploymentTriggerOnConfigChange,
				},
			},
			Template: &corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: label,
				},
				Spec: corev1.PodSpec{
					NodeSelector: cr.Spec.NodeSelector,
					Containers: []corev1.Container{
						{
							Name:  defaultName,
							Image: cr.Spec.ImagePullSpec,
							Ports: []corev1.ContainerPort{
								{
									ContainerPort: defaultPort,
									Protocol:      "TCP",
								},
							},
							Env:            env,
							VolumeMounts:   mounts,
							LivenessProbe:  generateLivenessProbeConfig(defaultPort, tls),
							ReadinessProbe: generateReadinessProbeConfig(defaultPort, tls),
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("100m"),
									corev1.ResourceMemory: resource.MustParse("256Mi"),
								},
							},
						},
					},
					Volumes:            volumes,
					ServiceAccountName: serviceAccountName,
					SecurityContext:    generateSecurityContext(cr),
				},
			},
		},
	}

	addOwnerRefToObject(dc, asOwner(cr))

	return dc, nil
}