package e2e_test

import (
	"context"
	"fmt"
	"math/rand"
	"testing"
	"time"

	"github.com/celo-org/celo-blockchain/core/types"
	"github.com/celo-org/celo-blockchain/test"
	"github.com/stretchr/testify/require"
)

func BenchmarkNet100EmptyBlocks(b *testing.B) {
	for _, n := range []int{1, 3, 9} {
		b.Run(fmt.Sprintf("%dNodes", n), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				accounts := test.Accounts(n)
				gc, ec, err := test.BuildConfig(accounts)
				require.NoError(b, err)
				network, err := test.NewNetwork(accounts, gc, ec)
				require.NoError(b, err)
				defer network.Shutdown()
				ctx, cancel := context.WithTimeout(context.Background(), time.Second*10*time.Duration(n))
				defer cancel()
				b.ResetTimer()
				err = network.AwaitBlock(ctx, 100)
				require.NoError(b, err)
			}
		})
	}
}

func BenchmarkNet1000Txs(b *testing.B) {
	// Seed the random number generator so that the generated numbers are
	// different on each run.
	rand.Seed(time.Now().UnixNano())
	for _, n := range []int{1, 3, 9} {
		b.Run(fmt.Sprintf("%dNodes", n), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				accounts := test.Accounts(n)
				gc, ec, err := test.BuildConfig(accounts)
				require.NoError(b, err)
				network, err := test.NewNetwork(accounts, gc, ec)
				require.NoError(b, err)
				defer network.Shutdown()
				ctx, cancel := context.WithTimeout(context.Background(), time.Second*10*time.Duration(n))
				defer cancel()
				b.ResetTimer()

				// Send 1000 txs randomly between nodes.
				txs := make([]*types.Transaction, 1000)
				for i := range txs {
					sender := network[rand.Intn(n)]
					receiver := network[rand.Intn(n)]
					tx, err := sender.SendCelo(ctx, receiver.DevAddress, 1)
					require.NoError(b, err)
					txs[i] = tx
				}
				err = network.AwaitTransactions(ctx, txs...)
				require.NoError(b, err)
				block := network[0].Tracker.GetProcessedBlockForTx(txs[len(txs)-1].Hash())
				fmt.Printf("Processed 1000 txs in %d blocks\n", block.NumberU64())
			}
		})
	}
}
