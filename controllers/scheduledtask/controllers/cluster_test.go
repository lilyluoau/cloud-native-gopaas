package controllers

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	gopaasv1 "github.com/gopaas/platform/controllers/scheduledtask/api/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/clientcmd"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Opt in only for a local test cluster with the ScheduledTask CRD installed.
func TestCronJobCluster(t *testing.T) {
	contextName := os.Getenv("GOPAAS_TEST_CONTEXT")
	if contextName == "" {
		t.Skip("set GOPAAS_TEST_CONTEXT to opt into a local cluster test")
	}
	cfg, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(clientcmd.NewDefaultClientConfigLoadingRules(), &clientcmd.ConfigOverrides{CurrentContext: contextName}).ClientConfig()
	if err != nil {
		t.Fatal(err)
	}
	scheme := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{batchv1.AddToScheme, corev1.AddToScheme, gopaasv1.AddToScheme} {
		if err := add(scheme); err != nil {
			t.Fatal(err)
		}
	}
	c, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 170*time.Second)
	defer cancel()
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("gopaas-cron-test-%d", time.Now().Unix())}}
	if err := c.Create(ctx, ns); err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		if err := c.Delete(cleanup, ns); err != nil {
			t.Error(err)
		}
	}()
	task := &gopaasv1.ScheduledTask{ObjectMeta: metav1.ObjectMeta{Name: "repeat", Namespace: ns.Name}, Spec: gopaasv1.ScheduledTaskSpec{Image: "busybox:1.36", Schedule: "* * * * *", Command: "echo recurring-ok"}}
	if err := c.Create(ctx, task); err != nil {
		t.Fatal(err)
	}
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: task.Name, Namespace: ns.Name}}
	r := &ScheduledTaskReconciler{Client: c, Scheme: scheme}
	if _, err := r.Reconcile(ctx, req); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for {
		var jobs batchv1.JobList
		if err := c.List(ctx, &jobs, client.InNamespace(ns.Name)); err != nil {
			t.Fatal(err)
		}
		for _, job := range jobs.Items {
			if job.Status.Succeeded > 0 && len(job.OwnerReferences) > 0 && job.OwnerReferences[0].Kind == "CronJob" {
				seen[job.Name] = true
			}
		}
		if len(seen) >= 2 {
			break
		}
		// Recreate the reconciler to verify it does not rely on process-local state.
		r = &ScheduledTaskReconciler{Client: c, Scheme: scheme}
		if _, err := r.Reconcile(ctx, req); err != nil {
			t.Fatal(err)
		}
		select {
		case <-ctx.Done():
			t.Fatal("did not observe two completed scheduled Jobs")
		case <-time.After(3 * time.Second):
		}
	}
	var crons batchv1.CronJobList
	if err := c.List(ctx, &crons, client.InNamespace(ns.Name)); err != nil {
		t.Fatal(err)
	}
	if len(crons.Items) != 1 {
		t.Fatalf("expected one CronJob, got %d", len(crons.Items))
	}
	t.Logf("observed %d distinct completed Jobs from one CronJob across repeated reconciliation", len(seen))
}
