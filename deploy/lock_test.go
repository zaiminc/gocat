package deploy

import (
	"context"
	"fmt"
	"os"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/stretchr/testify/require"
)

func TestLockUnlock(t *testing.T) {
	// Ensure you run `KUBECONFIG=gocat-test-kubeconfig kind export kubeconfig` before running this test
	kubeconfigPath := os.Getenv("GOCAT_TEST_KUBECONFIG")
	if kubeconfigPath == "" {
		t.Skip("GOCAT_TEST_KUBECONFIG is not set")
	}

	c := NewCoordinator("default", "gocat-test")
	defer func() {
		if c, _ := c.ClientSet(); c != nil {
			if err := c.CoreV1().ConfigMaps("default").Delete(context.Background(), "gocat-test", metav1.DeleteOptions{}); err != nil {
				t.Log(err)
			}
		}
	}()

	prevKubeconfig := os.Getenv("KUBECONFIG")
	os.Setenv("KUBECONFIG", kubeconfigPath)
	defer os.Setenv("KUBECONFIG", prevKubeconfig)

	ctx := context.Background()

	require.NoError(t, c.Lock(ctx, "myproject1", "prod", "user1", "for deployment of revision a"))
	require.ErrorIs(t, c.Lock(ctx, "myproject1", "prod", "user1", "for deployment of revision b"), ErrLocked)
	require.ErrorIs(t, c.Lock(ctx, "myproject1", "prod", "user2", "for deployment of revision b"), ErrLocked)
	require.ErrorIs(t, c.Unlock(ctx, "myproject1", "prod", "user2", false), newNotAllowedToUnlockError("user2"))
	require.NoError(t, c.Unlock(ctx, "myproject1", "prod", "user2", true))
	require.ErrorIs(t, c.Unlock(ctx, "myproject1", "prod", "user1", false), ErrAlreadyUnlocked)
	require.ErrorIs(t, c.Unlock(ctx, "myproject1", "prod", "user2", false), ErrAlreadyUnlocked)

	require.NoError(t, c.Lock(ctx, "myproject1", "prod", "user2", "for deployment of revision b"))
	require.NoError(t, c.Unlock(ctx, "myproject1", "prod", "user2", false))
	require.ErrorIs(t, c.Unlock(ctx, "myproject1", "prod", "user1", false), ErrAlreadyUnlocked)
	require.ErrorIs(t, c.Unlock(ctx, "myproject1", "prod", "user2", false), ErrAlreadyUnlocked)

	for u := 3; u < 10; u++ {
		user := fmt.Sprintf("user%d", u)
		require.NoError(t, c.Lock(ctx, "myproject1", "prod", user, fmt.Sprintf("for deployment of revision c+%d", u)))
		require.NoError(t, c.Unlock(ctx, "myproject1", "prod", user, false))
	}
}
