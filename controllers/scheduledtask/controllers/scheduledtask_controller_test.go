package controllers

import (
	"context"
	"testing"

	gopaasv1 "github.com/gopaas/platform/controllers/scheduledtask/api/v1"
	batchv1 "k8s.io/api/batch/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func setup(t *testing.T, extra ...client.Object) (*ScheduledTaskReconciler, ctrl.Request) {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := batchv1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := gopaasv1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	task := &gopaasv1.ScheduledTask{ObjectMeta: metav1.ObjectMeta{Name: "hourly", Namespace: "test", UID: "task-uid"},
		Spec: gopaasv1.ScheduledTaskSpec{Image: "busybox:1.36", Schedule: "0 * * * *", Command: "echo hello"}}
	objects := append([]client.Object{task}, extra...)
	c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(task).WithObjects(objects...).Build()
	return &ScheduledTaskReconciler{Client: c, Scheme: scheme}, ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "test", Name: "hourly"}}
}

func TestRecurringTaskReconcileAndUpdate(t *testing.T) {
	r, req := setup(t)
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		if _, err := r.Reconcile(ctx, req); err != nil {
			t.Fatal(err)
		}
	}
	var jobs batchv1.CronJobList
	if err := r.List(ctx, &jobs); err != nil {
		t.Fatal(err)
	}
	if len(jobs.Items) != 1 {
		t.Fatalf("duplicate CronJobs: %d", len(jobs.Items))
	}
	cron := jobs.Items[0]
	if cron.Spec.Schedule != "0 * * * *" || cron.Spec.ConcurrencyPolicy != batchv1.ForbidConcurrent || len(cron.OwnerReferences) != 1 {
		t.Fatalf("incorrect scheduled resource: %+v", cron)
	}
	var task gopaasv1.ScheduledTask
	if err := r.Get(ctx, req.NamespacedName, &task); err != nil {
		t.Fatal(err)
	}
	task.Spec.Schedule = "*/5 * * * *"
	task.Spec.Image = "alpine:3.19"
	if err := r.Update(ctx, &task); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Reconcile(ctx, req); err != nil {
		t.Fatal(err)
	}
	if err := r.Get(ctx, req.NamespacedName, &cron); err != nil {
		t.Fatal(err)
	}
	if cron.Spec.Schedule != task.Spec.Schedule || cron.Spec.JobTemplate.Spec.Template.Spec.Containers[0].Image != task.Spec.Image {
		t.Fatal("spec changes did not propagate")
	}
	if err := r.Get(ctx, req.NamespacedName, &task); err != nil {
		t.Fatal(err)
	}
	if task.Status.Phase != "Scheduled" || task.Status.LastScheduleTime != nil {
		t.Fatal("invented execution status")
	}
}

func TestDoesNotAdoptUnrelatedCronJob(t *testing.T) {
	r, req := setup(t, &batchv1.CronJob{ObjectMeta: metav1.ObjectMeta{Name: "hourly", Namespace: "test"}})
	if _, err := r.Reconcile(context.Background(), req); err == nil {
		t.Fatal("adopted an unrelated resource")
	}
}

func TestMissingTaskIsNoOp(t *testing.T) {
	r, _ := setup(t)
	if _, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Name: "missing", Namespace: "test"}}); err != nil {
		t.Fatal(err)
	}
}
