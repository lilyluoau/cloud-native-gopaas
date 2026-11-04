package store

import (
	"context"
	"errors"
	schedulerv1 "github.com/gopaas/platform/api/gen/scheduler/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	ktesting "k8s.io/client-go/testing"
	"testing"
)

func TestPermissionFailureDoesNotCreateFallbackWorkload(t *testing.T) {
	dyn := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme())
	denied := apierrors.NewForbidden(schema.GroupResource{Group: "gopaas.io", Resource: "scheduledtasks"}, "example", errors.New("denied"))
	dyn.PrependReactor("create", "scheduledtasks", func(ktesting.Action) (bool, runtime.Object, error) { return true, nil, denied })
	store := NewK8sStore(nil, dyn)
	_, err := store.Submit(context.Background(), &schedulerv1.SubmitTaskRequest{Name: "example", Namespace: "default", Image: "busybox:1.36", Schedule: "* * * * *"})
	if !apierrors.IsForbidden(err) {
		t.Fatalf("permission failure was hidden: %v", err)
	}
	if len(dyn.Actions()) != 1 || dyn.Actions()[0].GetResource() != scheduledTaskGVR {
		t.Fatalf("unexpected fallback action: %v", dyn.Actions())
	}
}
