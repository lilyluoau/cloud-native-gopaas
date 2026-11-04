package controllers

import (
	"context"
	"fmt"
	"reflect"
	"time"

	gopaasv1 "github.com/gopaas/platform/controllers/scheduledtask/api/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

// ScheduledTaskReconciler delegates recurring execution to Kubernetes CronJobs.
type ScheduledTaskReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

func (r *ScheduledTaskReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var task gopaasv1.ScheduledTask
	if err := r.Get(ctx, req.NamespacedName, &task); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if !task.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}
	cron := &batchv1.CronJob{ObjectMeta: metav1.ObjectMeta{Name: task.Name, Namespace: task.Namespace}}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, cron, func() error {
		if cron.ResourceVersion != "" && !metav1.IsControlledBy(cron, &task) {
			return fmt.Errorf("CronJob %s is owned by another resource", cron.Name)
		}
		if len(task.Name) > 52 || task.Spec.Image == "" || task.Spec.Schedule == "" {
			return fmt.Errorf("task requires image, schedule, and a name of at most 52 characters")
		}
		cmd := task.Spec.Command
		if cmd == "" {
			cmd = "echo scheduled-task && date"
		}
		successful, failed, deadline, backoff, ttl := int32(2), int32(2), int64(60), int32(2), int32(3600)
		zone := "Etc/UTC"
		cron.Spec.Schedule = task.Spec.Schedule
		cron.Spec.TimeZone = &zone
		cron.Spec.ConcurrencyPolicy = batchv1.ForbidConcurrent
		cron.Spec.StartingDeadlineSeconds = &deadline
		cron.Spec.SuccessfulJobsHistoryLimit = &successful
		cron.Spec.FailedJobsHistoryLimit = &failed
		cron.Spec.JobTemplate = batchv1.JobTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"gopaas.io/scheduledtask": task.Name}},
			Spec: batchv1.JobSpec{BackoffLimit: &backoff, TTLSecondsAfterFinished: &ttl,
				Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{
					RestartPolicy: corev1.RestartPolicyNever,
					Containers:    []corev1.Container{{Name: "task", Image: task.Spec.Image, Command: []string{"sh", "-c", cmd}}},
				}},
			},
		}
		return ctrl.SetControllerReference(&task, cron, r.Scheme)
	})
	if err != nil {
		if statusErr := r.setStatus(ctx, &task, "Error", "", err.Error(), nil); statusErr != nil {
			return ctrl.Result{}, fmt.Errorf("reconcile: %v; status: %w", err, statusErr)
		}
		return ctrl.Result{}, err
	}
	// Retire only the owned one-shot Job created by the previous controller.
	var legacy batchv1.Job
	err = r.Get(ctx, client.ObjectKey{Namespace: task.Namespace, Name: task.Name + "-job"}, &legacy)
	if err == nil && metav1.IsControlledBy(&legacy, &task) {
		if err := r.Delete(ctx, &legacy, client.PropagationPolicy(metav1.DeletePropagationBackground)); client.IgnoreNotFound(err) != nil {
			return ctrl.Result{}, err
		}
	} else if err != nil && !apierrors.IsNotFound(err) {
		return ctrl.Result{}, err
	}
	active := ""
	phase := "Scheduled"
	if len(cron.Status.Active) > 0 {
		active = cron.Status.Active[0].Name
		phase = "Running"
	}
	if err := r.setStatus(ctx, &task, phase, active, "managed by CronJob "+cron.Name, cron.Status.LastScheduleTime); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: time.Minute}, nil
}

func (r *ScheduledTaskReconciler) setStatus(ctx context.Context, task *gopaasv1.ScheduledTask, phase, active, message string, last *metav1.Time) error {
	desired := gopaasv1.ScheduledTaskStatus{Phase: phase, Message: message, ActiveJob: active, LastScheduleTime: last}
	if reflect.DeepEqual(task.Status, desired) {
		return nil
	}
	before := task.DeepCopyObject().(*gopaasv1.ScheduledTask)
	task.Status = desired
	return r.Status().Patch(ctx, task, client.MergeFrom(before))
}

func (r *ScheduledTaskReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&gopaasv1.ScheduledTask{}).
		Owns(&batchv1.CronJob{}).
		Complete(r)
}
