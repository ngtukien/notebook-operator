/*
Copyright 2026.
*/

package controller

import (
	"context"
	"fmt"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apiresource "k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	labv1alpha1 "github.com/ngtukien/notebook-operator/api/v1alpha1"
)

const appLabelKey = "app"

// NotebookLabReconciler reconciles a NotebookLab object
type NotebookLabReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// Cấp quyền (RBAC) cho Operator để nó có thể thao tác với các Resource của K8s
// +kubebuilder:rbac:groups=lab.ngtukien.id.vn,resources=notebooklabs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=lab.ngtukien.id.vn,resources=notebooklabs/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=lab.ngtukien.id.vn,resources=notebooklabs/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=persistentvolumeclaims;services;secrets;events,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=networking.k8s.io,resources=ingresses,verbs=get;list;watch;create;update;patch;delete

func (r *NotebookLabReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// =========================================================================
	// 1. Fetch instance NotebookLab
	// =========================================================================
	notebook := &labv1alpha1.NotebookLab{}
	err := r.Get(ctx, req.NamespacedName, notebook)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		logger.Error(err, "Failed to get NotebookLab")
		return ctrl.Result{}, err
	}

	// =========================================================================
	// 1.5. Reconcile Lifecycle Policies (Auto-Purge & Auto-Shutdown)
	// =========================================================================
	purged, requeueAfter, err := r.reconcileLifecycle(ctx, notebook)
	if err != nil {
		return ctrl.Result{}, err
	}
	if purged {
		return ctrl.Result{}, nil
	}

	// =========================================================================
	// 2. Reconcile PVC (Workspace)
	// =========================================================================
	if notebook.Spec.Storage != nil && notebook.Spec.Storage.Workspace != nil {
		err = r.reconcileWorkspacePVC(ctx, notebook)
		if err != nil {
			return r.updateStatusError(ctx, notebook, "Failed to reconcile PVC", err)
		}
	}

	// =========================================================================
	// 3. Reconcile Deployment & Secret (Token)
	// =========================================================================
	err = r.reconcileJupyterDeployment(ctx, notebook)
	if err != nil {
		return r.updateStatusError(ctx, notebook, "Failed to reconcile Deployment", err)
	}

	// =========================================================================
	// 4. Reconcile Service & Ingress (Network)
	// =========================================================================
	err = r.reconcileNetworking(ctx, notebook)
	if err != nil {
		return r.updateStatusError(ctx, notebook, "Failed to reconcile Networking", err)
	}

	// =========================================================================
	// 5. Cập nhật Status
	// =========================================================================
	err = r.updateNotebookStatus(ctx, notebook)
	if err != nil {
		logger.Error(err, "Failed to update NotebookLab status")
		return ctrl.Result{}, err
	}

	return ctrl.Result{RequeueAfter: requeueAfter}, nil
}

func (r *NotebookLabReconciler) reconcileLifecycle(ctx context.Context, notebook *labv1alpha1.NotebookLab) (bool, time.Duration, error) {
	logger := log.FromContext(ctx)
	if notebook.Spec.Lifecycle == nil {
		return false, 0, nil
	}

	var requeueAfter time.Duration

	// 1. PurgeAfterInactiveDays
	if notebook.Spec.Lifecycle.PurgeAfterInactiveDays > 0 {
		lastActive := notebook.Status.LastActiveTime
		if lastActive == nil {
			lastActive = &notebook.CreationTimestamp
		}

		inactiveDuration := time.Since(lastActive.Time)
		maxInactiveDuration := time.Duration(notebook.Spec.Lifecycle.PurgeAfterInactiveDays) * 24 * time.Hour

		if inactiveDuration >= maxInactiveDuration {
			logger.Info("Purging NotebookLab due to inactivity purge policy",
				"Name", notebook.Name,
				"InactiveDays", inactiveDuration.Hours()/24,
				"ThresholdDays", notebook.Spec.Lifecycle.PurgeAfterInactiveDays)

			pvcName := notebook.Name + "-workspace"
			pvc := &corev1.PersistentVolumeClaim{}
			if err := r.Get(ctx, types.NamespacedName{Name: pvcName, Namespace: notebook.Namespace}, pvc); err == nil {
				if err := r.Delete(ctx, pvc); err != nil {
					logger.Error(err, "Failed to delete workspace PVC during purge", "PVC", pvcName)
				}
			}

			if err := r.Delete(ctx, notebook); err != nil {
				logger.Error(err, "Failed to purge NotebookLab resource", "Notebook", notebook.Name)
				return false, 0, err
			}

			return true, 0, nil
		}

		requeueAfter = maxInactiveDuration - inactiveDuration
	}

	// 2. IdleTimeoutMinutes
	if notebook.Spec.Lifecycle.IdleTimeoutMinutes > 0 && notebook.Status.Phase == labv1alpha1.PhaseRunning {
		lastActive := notebook.Status.LastActiveTime
		if lastActive == nil {
			lastActive = &notebook.CreationTimestamp
		}

		idleDuration := time.Since(lastActive.Time)
		maxIdleDuration := time.Duration(notebook.Spec.Lifecycle.IdleTimeoutMinutes) * time.Minute

		if idleDuration >= maxIdleDuration {
			logger.Info("Idle timeout reached. Pausing NotebookLab (replicas=0)",
				"Name", notebook.Name,
				"IdleMinutes", idleDuration.Minutes())

			patch := client.MergeFrom(notebook.DeepCopy())
			notebook.Spec.Replicas = ptr.To(int32(0))
			if err := r.Patch(ctx, notebook, patch); err != nil {
				logger.Error(err, "Failed to pause NotebookLab on idle timeout")
				return false, 0, err
			}
		} else {
			remainingIdle := maxIdleDuration - idleDuration
			if requeueAfter == 0 || remainingIdle < requeueAfter {
				requeueAfter = remainingIdle
			}
		}
	}

	// 3. MaxLifespanHours
	if notebook.Spec.Lifecycle.MaxLifespanHours > 0 && notebook.Status.Phase == labv1alpha1.PhaseRunning {
		runningDuration := time.Since(notebook.CreationTimestamp.Time)
		maxLifespanDuration := time.Duration(notebook.Spec.Lifecycle.MaxLifespanHours) * time.Hour

		if runningDuration >= maxLifespanDuration {
			logger.Info("Max lifespan reached. Pausing NotebookLab (replicas=0)",
				"Name", notebook.Name,
				"RunningHours", runningDuration.Hours())

			patch := client.MergeFrom(notebook.DeepCopy())
			notebook.Spec.Replicas = ptr.To(int32(0))
			if err := r.Patch(ctx, notebook, patch); err != nil {
				logger.Error(err, "Failed to pause NotebookLab on max lifespan")
				return false, 0, err
			}
		} else {
			remainingLifespan := maxLifespanDuration - runningDuration
			if requeueAfter == 0 || remainingLifespan < requeueAfter {
				requeueAfter = remainingLifespan
			}
		}
	}

	return false, requeueAfter, nil
}

func (r *NotebookLabReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&labv1alpha1.NotebookLab{}).
		Owns(&appsv1.Deployment{}).
		Owns(&corev1.PersistentVolumeClaim{}).
		Owns(&corev1.Service{}).
		Owns(&networkingv1.Ingress{}).
		Owns(&corev1.Secret{}).
		Complete(r)
}

func (r *NotebookLabReconciler) reconcileWorkspacePVC(ctx context.Context, notebook *labv1alpha1.NotebookLab) error {
	logger := log.FromContext(ctx)
	pvcName := notebook.Name + "-workspace"
	pvc := &corev1.PersistentVolumeClaim{}

	err := r.Get(ctx, types.NamespacedName{Name: pvcName, Namespace: notebook.Namespace}, pvc)
	if err != nil && apierrors.IsNotFound(err) {
		logger.Info("Creating a new Workspace PVC", "PVC.Namespace", notebook.Namespace, "PVC.Name", pvcName)

		newPVC := &corev1.PersistentVolumeClaim{
			ObjectMeta: metav1.ObjectMeta{
				Name:      pvcName,
				Namespace: notebook.Namespace,
				Labels:    notebook.Labels,
			},
			Spec: corev1.PersistentVolumeClaimSpec{
				AccessModes: []corev1.PersistentVolumeAccessMode{
					corev1.ReadWriteOnce,
				},
				Resources: corev1.VolumeResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceStorage: notebook.Spec.Storage.Workspace.Size,
					},
				},
			},
		}

		if notebook.Spec.Storage.Workspace.StorageClassName != "" {
			newPVC.Spec.StorageClassName = &notebook.Spec.Storage.Workspace.StorageClassName
		}

		if err := ctrl.SetControllerReference(notebook, newPVC, r.Scheme); err != nil {
			return err
		}

		if err := r.Create(ctx, newPVC); err != nil {
			logger.Error(err, "Failed to create new PVC", "PVC.Namespace", newPVC.Namespace, "PVC.Name", newPVC.Name)
			return err
		}
		return nil
	} else if err != nil {
		return err
	}

	logger.Info("Workspace PVC already exists", "PVC.Namespace", notebook.Namespace, "PVC.Name", pvcName)
	return nil
}

func (r *NotebookLabReconciler) reconcileNotebookSecret(ctx context.Context, notebook *labv1alpha1.NotebookLab) (string, error) {
	secretName := notebook.Name + "-secret"
	secret := &corev1.Secret{}

	err := r.Get(ctx, types.NamespacedName{Name: secretName, Namespace: notebook.Namespace}, secret)
	if err != nil && apierrors.IsNotFound(err) {
		token := notebook.Name + "-token-sec"

		newSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      secretName,
				Namespace: notebook.Namespace,
				Labels:    notebook.Labels,
			},
			StringData: map[string]string{
				"token": token,
			},
		}

		if err := ctrl.SetControllerReference(notebook, newSecret, r.Scheme); err != nil {
			return "", err
		}
		if err := r.Create(ctx, newSecret); err != nil {
			return "", err
		}
		return token, nil
	} else if err != nil {
		return "", err
	}

	return string(secret.Data["token"]), nil
}

func (r *NotebookLabReconciler) reconcileJupyterDeployment(ctx context.Context, notebook *labv1alpha1.NotebookLab) error {
	logger := log.FromContext(ctx)
	deployName := notebook.Name

	replicas := int32(1)
	if notebook.Spec.Replicas != nil {
		replicas = *notebook.Spec.Replicas
	}

	volumes := []corev1.Volume{}
	volumeMounts := []corev1.VolumeMount{}

	if notebook.Spec.Storage != nil && notebook.Spec.Storage.Workspace != nil {
		volumes = append(volumes, corev1.Volume{
			Name: "workspace",
			VolumeSource: corev1.VolumeSource{
				PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
					ClaimName: notebook.Name + "-workspace",
				},
			},
		})
		volumeMounts = append(volumeMounts, corev1.VolumeMount{
			Name:      "workspace",
			MountPath: notebook.Spec.Storage.Workspace.MountPath,
		})
	}

	if notebook.Spec.Storage != nil && len(notebook.Spec.Storage.Datasets) > 0 {
		for _, ds := range notebook.Spec.Storage.Datasets {
			volumes = append(volumes, corev1.Volume{
				Name: ds.Name,
				VolumeSource: corev1.VolumeSource{
					PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
						ClaimName: ds.PVCName,
						ReadOnly:  true,
					},
				},
			})
			volumeMounts = append(volumeMounts, corev1.VolumeMount{
				Name:      ds.Name,
				MountPath: ds.MountPath,
				ReadOnly:  true,
			})
		}
	}

	emptyDirSource := &corev1.EmptyDirVolumeSource{}
	tmpMountPath := "/tmp"

	if notebook.Spec.Storage != nil && notebook.Spec.Storage.Tmp != nil {
		if !notebook.Spec.Storage.Tmp.SizeLimit.IsZero() {
			emptyDirSource.SizeLimit = &notebook.Spec.Storage.Tmp.SizeLimit
		}
		if notebook.Spec.Storage.Tmp.MountPath != "" {
			tmpMountPath = notebook.Spec.Storage.Tmp.MountPath
		}
	}

	volumes = append(volumes, corev1.Volume{
		Name: "tmp",
		VolumeSource: corev1.VolumeSource{
			EmptyDir: emptyDirSource,
		},
	})
	volumeMounts = append(volumeMounts, corev1.VolumeMount{
		Name:      "tmp",
		MountPath: tmpMountPath,
	})

	tolerations := notebook.Spec.Tolerations
	if notebook.Spec.GPU != nil && notebook.Spec.GPU.Enable {
		if len(tolerations) == 0 {
			tolerations = append(tolerations, corev1.Toleration{
				Key:      "nvidia.com/gpu",
				Operator: corev1.TolerationOpExists,
				Effect:   corev1.TaintEffectNoSchedule,
			})
		}
	}

	_, err := r.reconcileNotebookSecret(ctx, notebook)
	if err != nil {
		return err
	}

	runAsNonRoot := true
	runAsUser := int64(1000)
	runAsGroup := int64(1000)
	fsGroup := int64(1000)
	autoMountToken := false
	allowPrivilegeEscalation := false
	gpuEnabled := notebook.Spec.GPU != nil && notebook.Spec.GPU.Enable

	podSecurityContext := &corev1.PodSecurityContext{
		RunAsNonRoot: &runAsNonRoot,
		RunAsUser:    &runAsUser,
		RunAsGroup:   &runAsGroup,
		FSGroup:      &fsGroup,
		SeccompProfile: &corev1.SeccompProfile{
			Type: corev1.SeccompProfileTypeRuntimeDefault,
		},
	}

	containerSecurityContext := &corev1.SecurityContext{
		AllowPrivilegeEscalation: &allowPrivilegeEscalation,
		Capabilities: &corev1.Capabilities{
			Drop: []corev1.Capability{"ALL"},
		},
	}

	// GPU containers need relaxed security for NVIDIA runtime to inject device nodes
	if gpuEnabled {
		allowEscalation := true
		containerSecurityContext = &corev1.SecurityContext{
			AllowPrivilegeEscalation: &allowEscalation,
		}
	}

	containerResources := *notebook.Spec.Resources.DeepCopy()
	if containerResources.Limits == nil {
		containerResources.Limits = make(corev1.ResourceList)
	}
	if containerResources.Requests == nil {
		containerResources.Requests = make(corev1.ResourceList)
	}

	podSpec := corev1.PodSpec{
		AutomountServiceAccountToken: &autoMountToken,
		SecurityContext:              podSecurityContext,
		Containers: []corev1.Container{
			{
				Name:            "jupyter",
				Image:           notebook.Spec.Image,
				Resources:       containerResources,

				VolumeMounts:    volumeMounts,
				SecurityContext: containerSecurityContext,
				Env: []corev1.EnvVar{
					{
						Name: "JUPYTER_TOKEN",
						ValueFrom: &corev1.EnvVarSource{
							SecretKeyRef: &corev1.SecretKeySelector{
								LocalObjectReference: corev1.LocalObjectReference{Name: notebook.Name + "-secret"},
								Key:                  "token",
							},
						},
					},
				},
			},
		},
		Volumes:          volumes,
		NodeSelector:     notebook.Spec.NodeSelector,
		Tolerations:      tolerations,
		ImagePullSecrets: notebook.Spec.ImagePullSecrets,
	}



	// Device Plugin: Inject GPU resource limits based on provider type.
	// - hami: uses nvidia.com/gpu + optional nvidia.com/gpumem + nvidia.com/gpucores
	//         (HAMi device plugin intercepts and enforces vGPU memory/core limits)
	// - mig:  uses nvidia.com/mig-<profile> extended resource
	//         (NVIDIA MIG device plugin exposes each partition as a discrete resource)
	if gpuEnabled {
		r.injectGPUResources(&podSpec.Containers[0], notebook.Spec.GPU)
	}

	deploy := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      deployName,
			Namespace: notebook.Namespace,
			Labels:    notebook.Labels,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Strategy: appsv1.DeploymentStrategy{
				Type: appsv1.RecreateDeploymentStrategyType,
			},
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{appLabelKey: deployName},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{appLabelKey: deployName},
				},
				Spec: podSpec,
			},
		},
	}

	if err := ctrl.SetControllerReference(notebook, deploy, r.Scheme); err != nil {
		return err
	}

	existingDeploy := &appsv1.Deployment{}
	err = r.Get(ctx, types.NamespacedName{Name: deployName, Namespace: notebook.Namespace}, existingDeploy)
	if err != nil && apierrors.IsNotFound(err) {
		logger.Info("Creating a new Jupyter Deployment", "Namespace", deploy.Namespace, "Name", deploy.Name)
		return r.Create(ctx, deploy)
	} else if err == nil {
		patch := client.MergeFrom(existingDeploy.DeepCopy())
		existingDeploy.Labels = deploy.Labels
		existingDeploy.Spec.Replicas = deploy.Spec.Replicas
		existingDeploy.Spec.Strategy = deploy.Spec.Strategy
		existingDeploy.Spec.Template = deploy.Spec.Template
		return r.Patch(ctx, existingDeploy, patch)
	}

	return err
}

// injectGPUResources adds the appropriate extended resource limits to the container
// depending on the GPU provider:
//
//   - hami: requests nvidia.com/gpu=1 (required for HAMi device plugin to bind the physical GPU)
//     and optionally nvidia.com/gpumem (MiB) + nvidia.com/gpucores (%) so that the
//     HAMi vGPU scheduler enforces memory and compute isolation.
//
//   - mig: requests nvidia.com/mig-<profile>=1 (e.g. nvidia.com/mig-1g.5gb) which is
//     the extended resource exposed by the NVIDIA MIG device plugin for each partition.
//
//   - (default/standard): falls back to nvidia.com/gpu=1 (full GPU via standard plugin).
func (r *NotebookLabReconciler) injectGPUResources(container *corev1.Container, gpu *labv1alpha1.GPUSpec) {
	if container.Resources.Limits == nil {
		container.Resources.Limits = corev1.ResourceList{}
	}

	switch gpu.Type {
	case "mig":
		// MIG device plugin: each partition is a distinct extended resource.
		// Profile must be specified (e.g. "1g.5gb").
		if gpu.MIG != nil && gpu.MIG.Profile != "" {
			resourceName := corev1.ResourceName(fmt.Sprintf("nvidia.com/mig-%s", gpu.MIG.Profile))
			container.Resources.Limits[resourceName] = *apiresource.NewQuantity(1, apiresource.DecimalSI)
		}

	default:
		// HAMi (and standard nvidia device plugin): request 1 vGPU device.
		container.Resources.Limits[corev1.ResourceName("nvidia.com/gpu")] = *apiresource.NewQuantity(1, apiresource.DecimalSI)

		// HAMi vGPU memory and core isolation.
		if gpu.HAMi != nil {
			if !gpu.HAMi.Memory.IsZero() {
				memMiB := gpu.HAMi.Memory.Value() / (1024 * 1024)
				container.Resources.Limits[corev1.ResourceName("nvidia.com/gpumem")] = *apiresource.NewQuantity(memMiB, apiresource.DecimalSI)
				setOrUpdateEnv(&container.Env, "CUDA_VGPU_MEM", fmt.Sprintf("%d", memMiB))
			}
			if gpu.HAMi.Cores > 0 {
				container.Resources.Limits[corev1.ResourceName("nvidia.com/gpucores")] = *apiresource.NewQuantity(int64(gpu.HAMi.Cores), apiresource.DecimalSI)
				setOrUpdateEnv(&container.Env, "CUDA_VGPU_CORES", fmt.Sprintf("%d", gpu.HAMi.Cores))
			}
		}
	}

}

func setOrUpdateEnv(envVars *[]corev1.EnvVar, name, value string) {
	for i, e := range *envVars {
		if e.Name == name {
			(*envVars)[i].Value = value
			return
		}
	}
	*envVars = append(*envVars, corev1.EnvVar{Name: name, Value: value})
}

func (r *NotebookLabReconciler) reconcileNetworking(ctx context.Context, notebook *labv1alpha1.NotebookLab) error {
	logger := log.FromContext(ctx)

	svcName := notebook.Name + "-svc"
	svc := &corev1.Service{}
	err := r.Get(ctx, types.NamespacedName{Name: svcName, Namespace: notebook.Namespace}, svc)
	if err != nil && apierrors.IsNotFound(err) {
		svc = &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Name:      svcName,
				Namespace: notebook.Namespace,
				Labels:    notebook.Labels,
			},
			Spec: corev1.ServiceSpec{
				Selector: map[string]string{appLabelKey: notebook.Name},
				Ports: []corev1.ServicePort{
					{
						Name: "http",
						Port: 8888,
					},
				},
			},
		}
		if err := ctrl.SetControllerReference(notebook, svc, r.Scheme); err != nil {
			return err
		}
		logger.Info("Creating Service", "Name", svcName)
		if err := r.Create(ctx, svc); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}

	ingName := notebook.Name + "-ingress"
	ing := &networkingv1.Ingress{}
	err = r.Get(ctx, types.NamespacedName{Name: ingName, Namespace: notebook.Namespace}, ing)
	if err != nil && apierrors.IsNotFound(err) {
		pathType := networkingv1.PathTypePrefix
		host := notebook.Name + ".local"

		ing = &networkingv1.Ingress{
			ObjectMeta: metav1.ObjectMeta{
				Name:      ingName,
				Namespace: notebook.Namespace,
				Labels:    notebook.Labels,
				Annotations: map[string]string{
					"nginx.ingress.kubernetes.io/proxy-read-timeout": "3600",
					"nginx.ingress.kubernetes.io/proxy-send-timeout": "3600",
					"nginx.ingress.kubernetes.io/websocket-services": svcName,
				},
			},
			Spec: networkingv1.IngressSpec{
				Rules: []networkingv1.IngressRule{
					{
						Host: host,
						IngressRuleValue: networkingv1.IngressRuleValue{
							HTTP: &networkingv1.HTTPIngressRuleValue{
								Paths: []networkingv1.HTTPIngressPath{
									{
										Path:     "/",
										PathType: &pathType,
										Backend: networkingv1.IngressBackend{
											Service: &networkingv1.IngressServiceBackend{
												Name: svcName,
												Port: networkingv1.ServiceBackendPort{
													Number: 8888,
												},
											},
										},
									},
								},
							},
						},
					},
				},
			},
		}
		if err := ctrl.SetControllerReference(notebook, ing, r.Scheme); err != nil {
			return err
		}
		logger.Info("Creating Ingress", "Name", ingName)
		if err := r.Create(ctx, ing); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}

	return nil
}

func (r *NotebookLabReconciler) updateNotebookStatus(ctx context.Context, notebook *labv1alpha1.NotebookLab) error {
	deploy := &appsv1.Deployment{}
	err := r.Get(ctx, types.NamespacedName{Name: notebook.Name, Namespace: notebook.Namespace}, deploy)
	if err != nil {
		if apierrors.IsNotFound(err) {
			notebook.Status.Phase = labv1alpha1.PhaseProvisioning
			return r.Status().Update(ctx, notebook)
		}
		return err
	}

	replicas := int32(1)
	if notebook.Spec.Replicas != nil {
		replicas = *notebook.Spec.Replicas
	}

	if replicas == 0 {
		if deploy.Status.Replicas == 0 {
			notebook.Status.Phase = labv1alpha1.PhasePaused
		} else {
			notebook.Status.Phase = labv1alpha1.PhasePausing
		}
	} else {
		if deploy.Status.ReadyReplicas > 0 {
			notebook.Status.Phase = labv1alpha1.PhaseRunning
		} else {
			notebook.Status.Phase = labv1alpha1.PhaseProvisioning
		}
	}

	token, err := r.reconcileNotebookSecret(ctx, notebook)
	if err != nil {
		token = notebook.Name + "-token-sec"
	}
	notebook.Status.PVCName = notebook.Name + "-workspace"
	notebook.Status.AccessURL = "https://" + notebook.Name + ".local/lab?token=" + token

	return r.Status().Update(ctx, notebook)
}

func (r *NotebookLabReconciler) updateStatusError(ctx context.Context, notebook *labv1alpha1.NotebookLab, msg string, err error) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	logger.Error(err, "Reconciliation failed", "message", msg)
	notebook.Status.Phase = labv1alpha1.PhaseFailed
	if updateErr := r.Status().Update(ctx, notebook); updateErr != nil {
		logger.Error(updateErr, "Failed to update NotebookLab status on error")
	}
	return ctrl.Result{}, err
}
