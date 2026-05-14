package consumer

import (
	"fmt"
	"sync/atomic"

	"aegis/platform/jwtkeys"
	"aegis/platform/crypto"

	"go.uber.org/fx"
)

var signerRef atomic.Pointer[jwtkeys.Signer]

// JWTSignerModule registers an fx Invoke hook that copies the resolved
// *jwtkeys.Signer into a package-level pointer so the free-function task
// executors (build_datapack.go / algo_execution.go) can mint service tokens
// without each call site receiving a Signer dependency.
var JWTSignerModule = fx.Module("consumer.jwtsigner",
	fx.Invoke(func(s *jwtkeys.Signer) {
		signerRef.Store(s)
	}),
)

func issueServiceToken(taskID string) (string, error) {
	s := signerRef.Load()
	if s == nil {
		return "", fmt.Errorf("jwt signer not initialized")
	}
	token, _, err := crypto.GenerateServiceToken(taskID, s.PrivateKey, s.Kid)
	return token, err
}
