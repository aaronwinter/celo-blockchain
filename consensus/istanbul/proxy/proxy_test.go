package proxy_test

import (
	"os"
	"testing"

	"github.com/aaronwinter/celo-blockchain/consensus/istanbul/backend"
	"github.com/aaronwinter/celo-blockchain/consensus/istanbul/backend/backendtest"
)

func TestMain(m *testing.M) {
	backendtest.InitTestBackendFactory(backend.TestBackendFactory)
	code := m.Run()
	os.Exit(code)
}
