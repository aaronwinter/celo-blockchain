// Copyright 2017 The go-ethereum Authors
// This file is part of the go-ethereum library.
//
// The go-ethereum library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The go-ethereum library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the go-ethereum library. If not, see <http://www.gnu.org/licenses/>.

package keystore

import (
	"crypto/ecdsa"
	"math/big"

	blscrypto "github.com/aaronwinter/celo-blockchain/crypto/bls"

	ethereum "github.com/aaronwinter/celo-blockchain"
	"github.com/aaronwinter/celo-blockchain/accounts"
	"github.com/aaronwinter/celo-blockchain/common"
	"github.com/aaronwinter/celo-blockchain/core/types"
	"github.com/aaronwinter/celo-blockchain/crypto"
	"github.com/aaronwinter/celo-blockchain/log"
)

// keystoreWallet implements the accounts.Wallet interface for the original
// keystore.
type keystoreWallet struct {
	account  accounts.Account // Single account contained in this wallet
	keystore *KeyStore        // Keystore where the account originates from
}

// URL implements accounts.Wallet, returning the URL of the account within.
func (w *keystoreWallet) URL() accounts.URL {
	return w.account.URL
}

// Status implements accounts.Wallet, returning whether the account held by the
// keystore wallet is unlocked or not.
func (w *keystoreWallet) Status() (string, error) {
	w.keystore.mu.RLock()
	defer w.keystore.mu.RUnlock()

	if _, ok := w.keystore.unlocked[w.account.Address]; ok {
		return "Unlocked", nil
	}
	return "Locked", nil
}

// Open implements accounts.Wallet, but is a noop for plain wallets since there
// is no connection or decryption step necessary to access the list of accounts.
func (w *keystoreWallet) Open(passphrase string) error { return nil }

// Close implements accounts.Wallet, but is a noop for plain wallets since there
// is no meaningful open operation.
func (w *keystoreWallet) Close() error { return nil }

// Accounts implements accounts.Wallet, returning an account list consisting of
// a single account that the plain kestore wallet contains.
func (w *keystoreWallet) Accounts() []accounts.Account {
	return []accounts.Account{w.account}
}

// Contains implements accounts.Wallet, returning whether a particular account is
// or is not wrapped by this wallet instance.
func (w *keystoreWallet) Contains(account accounts.Account) bool {
	return account.Address == w.account.Address && (account.URL == (accounts.URL{}) || account.URL == w.account.URL)
}

// Decrypt decrypts an ECIES ciphertext.
func (w *keystoreWallet) Decrypt(account accounts.Account, c, s1, s2 []byte) ([]byte, error) {
	if account.Address != w.account.Address {
		log.Debug(accounts.ErrUnknownAccount.Error(), "account", account)
		return nil, accounts.ErrUnknownAccount
	}
	if account.URL != (accounts.URL{}) && account.URL != w.account.URL {
		log.Debug(accounts.ErrUnknownAccount.Error(), "account", account)
		return nil, accounts.ErrUnknownAccount
	}
	// Account seems valid, request the keystore to sign
	return w.keystore.Decrypt(account, c, s1, s2)
}

// Derive implements accounts.Wallet, but is a noop for plain wallets since there
// is no notion of hierarchical account derivation for plain keystore accounts.
func (w *keystoreWallet) Derive(path accounts.DerivationPath, pin bool) (accounts.Account, error) {
	return accounts.Account{}, accounts.ErrNotSupported
}

// ConfirmAddress implements accounts.Wallet, but is a noop for plain wallets since there
// is no notion of address confirmation for plain keystore accounts.
func (w *keystoreWallet) ConfirmAddress(path accounts.DerivationPath) (common.Address, error) {
	return common.Address{}, accounts.ErrNotSupported
}

// SelfDerive implements accounts.Wallet, but is a noop for plain wallets since
// there is no notion of hierarchical account derivation for plain keystore accounts.
func (w *keystoreWallet) SelfDerive(bases []accounts.DerivationPath, chain ethereum.ChainStateReader) {
}

// signHash attempts to sign the given hash with
// the given account. If the wallet does not wrap this particular account, an
// error is returned to avoid account leakage (even though in theory we may be
// able to sign via our shared keystore backend).
func (w *keystoreWallet) signHash(account accounts.Account, hash []byte) ([]byte, error) {
	// Make sure the requested account is contained within
	if !w.Contains(account) {
		log.Debug(accounts.ErrUnknownAccount.Error(), "account", account)
		return nil, accounts.ErrUnknownAccount
	}
	// Account seems valid, request the keystore to sign
	return w.keystore.SignHash(account, hash)
}

func (w *keystoreWallet) GetPublicKey(account accounts.Account) (*ecdsa.PublicKey, error) {
	// Make sure the requested account is contained within
	if !w.Contains(account) {
		log.Debug(accounts.ErrUnknownAccount.Error(), "account", account)
		return nil, accounts.ErrUnknownAccount
	}
	// Account seems valid, request the public key
	return w.keystore.GetPublicKey(account)
}

func (w *keystoreWallet) SignBLS(account accounts.Account, msg []byte, extraData []byte, useComposite, cip22 bool) (blscrypto.SerializedSignature, error) {
	// Make sure the requested account is contained within
	if !w.Contains(account) {
		log.Debug(accounts.ErrUnknownAccount.Error(), "account", account)
		return blscrypto.SerializedSignature{}, accounts.ErrUnknownAccount
	}
	// Account seems valid, request the keystore to sign
	return w.keystore.SignBLS(account, msg, extraData, useComposite, cip22)
}

func (w *keystoreWallet) GenerateProofOfPossession(account accounts.Account, address common.Address) ([]byte, []byte, error) {
	// Make sure the requested account is contained within
	if !w.Contains(account) {
		log.Debug(accounts.ErrUnknownAccount.Error(), "account", account)
		return nil, nil, accounts.ErrUnknownAccount
	}
	// Account seems valid, request the keystore to sign
	return w.keystore.GenerateProofOfPossession(account, address)
}

func (w *keystoreWallet) GenerateProofOfPossessionBLS(account accounts.Account, address common.Address) ([]byte, []byte, error) {
	// Make sure the requested account is contained within
	if !w.Contains(account) {
		log.Debug(accounts.ErrUnknownAccount.Error(), "account", account)
		return nil, nil, accounts.ErrUnknownAccount
	}
	// Account seems valid, request the keystore to sign
	return w.keystore.GenerateProofOfPossessionBLS(account, address)
}

// SignData signs keccak256(data). The mimetype parameter describes the type of data being signed
func (w *keystoreWallet) SignData(account accounts.Account, mimeType string, data []byte) ([]byte, error) {
	return w.signHash(account, crypto.Keccak256(data))
}

// SignHash implements accounts.Wallet, attempting to sign the given hash with
// the given account. If the wallet does not wrap this particular account, an
// error is returned to avoid account leakage (even though in theory we may be
// able to sign via our shared keystore backend).
//
// DEPRECATED, use SignData in future releases.
func (w *keystoreWallet) SignHash(account accounts.Account, hash []byte) ([]byte, error) {
	return w.signHash(account, hash)
}

// SignDataWithPassphrase signs keccak256(data). The mimetype parameter describes the type of data being signed
func (w *keystoreWallet) SignDataWithPassphrase(account accounts.Account, passphrase, mimeType string, data []byte) ([]byte, error) {
	// Make sure the requested account is contained within
	if !w.Contains(account) {
		log.Debug(accounts.ErrUnknownAccount.Error(), "account", account)
		return nil, accounts.ErrUnknownAccount
	}
	// Account seems valid, request the keystore to sign
	return w.keystore.SignHashWithPassphrase(account, passphrase, crypto.Keccak256(data))
}

func (w *keystoreWallet) SignText(account accounts.Account, text []byte) ([]byte, error) {
	return w.signHash(account, accounts.TextHash(text))
}

// SignTextWithPassphrase implements accounts.Wallet, attempting to sign the
// given hash with the given account using passphrase as extra authentication.
func (w *keystoreWallet) SignTextWithPassphrase(account accounts.Account, passphrase string, text []byte) ([]byte, error) {
	// Make sure the requested account is contained within
	if !w.Contains(account) {
		return nil, accounts.ErrUnknownAccount
	}
	// Account seems valid, request the keystore to sign
	return w.keystore.SignHashWithPassphrase(account, passphrase, accounts.TextHash(text))
}

// SignTx implements accounts.Wallet, attempting to sign the given transaction
// with the given account. If the wallet does not wrap this particular account,
// an error is returned to avoid account leakage (even though in theory we may
// be able to sign via our shared keystore backend).
func (w *keystoreWallet) SignTx(account accounts.Account, tx *types.Transaction, chainID *big.Int) (*types.Transaction, error) {
	// Make sure the requested account is contained within
	if !w.Contains(account) {
		log.Debug(accounts.ErrUnknownAccount.Error(), "account", account)
		return nil, accounts.ErrUnknownAccount
	}
	// Account seems valid, request the keystore to sign
	return w.keystore.SignTx(account, tx, chainID)
}

// SignTxWithPassphrase implements accounts.Wallet, attempting to sign the given
// transaction with the given account using passphrase as extra authentication.
func (w *keystoreWallet) SignTxWithPassphrase(account accounts.Account, passphrase string, tx *types.Transaction, chainID *big.Int) (*types.Transaction, error) {
	// Make sure the requested account is contained within
	if !w.Contains(account) {
		log.Debug(accounts.ErrUnknownAccount.Error(), "account", account)
		return nil, accounts.ErrUnknownAccount
	}
	// Account seems valid, request the keystore to sign
	return w.keystore.SignTxWithPassphrase(account, passphrase, tx, chainID)
}
