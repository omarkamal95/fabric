/*
Copyright IBM Corp. 2016 All Rights Reserved.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

		 http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/
package sw

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rsa"
	"crypto/x509"
	"errors"
	"fmt"
	"hash"
	"reflect"

	"crypto/sha256"
	"crypto/sha512"

	"github.com/hyperledger/fabric/bccsp"
	"github.com/hyperledger/fabric/bccsp/utils"
	"github.com/hyperledger/fabric/common/flogging"
	"golang.org/x/crypto/sha3"
)

var (
	logger = flogging.MustGetLogger("bccsp_sw")
)

// NewDefaultSecurityLevel returns a new instance of the software-based BCCSP
// at security level 256, hash family SHA2 and using FolderBasedKeyStore as KeyStore.
func NewDefaultSecurityLevel(keyStorePath string) (bccsp.BCCSP, error) {
	ks := &fileBasedKeyStore{}
	if err := ks.Init(nil, keyStorePath, false); err != nil {
		return nil, fmt.Errorf("Failed initializing key store [%s]", err)
	}

	return New(256, "SHA2", ks)
}

// NewDefaultSecurityLevel returns a new instance of the software-based BCCSP
// at security level 256, hash family SHA2 and using the passed KeyStore.
func NewDefaultSecurityLevelWithKeystore(keyStore bccsp.KeyStore) (bccsp.BCCSP, error) {
	return New(256, "SHA2", keyStore)
}

// New returns a new instance of the software-based BCCSP
// set at the passed security level, hash family and KeyStore.
func New(securityLevel int, hashFamily string, keyStore bccsp.KeyStore) (bccsp.BCCSP, error) {
	// Init config
	conf := &config{}
	err := conf.setSecurityLevel(securityLevel, hashFamily)
	if err != nil {
		return nil, fmt.Errorf("Failed initializing configuration [%s]", err)
	}

	// Check KeyStore
	if keyStore == nil {
		return nil, errors.New("Invalid bccsp.KeyStore instance. It must be different from nil.")
	}

	// Set the encryptors
	encryptors := make(map[reflect.Type]Encryptor)
	encryptors[reflect.TypeOf(&aesPrivateKey{})] = &aescbcpkcs7Encryptor{}

	// Set the decryptors
	decryptors := make(map[reflect.Type]Decryptor)
	decryptors[reflect.TypeOf(&aesPrivateKey{})] = &aescbcpkcs7Decryptor{}

	// Set the signers
	signers := make(map[reflect.Type]Signer)
	signers[reflect.TypeOf(&ecdsaPrivateKey{})] = &ecdsaSigner{}
	signers[reflect.TypeOf(&rsaPrivateKey{})] = &rsaSigner{}

	// Set the verifiers
	verifiers := make(map[reflect.Type]Verifier)
	verifiers[reflect.TypeOf(&ecdsaPrivateKey{})] = &ecdsaPrivateKeyVerifier{}
	verifiers[reflect.TypeOf(&ecdsaPublicKey{})] = &ecdsaPublicKeyKeyVerifier{}
	verifiers[reflect.TypeOf(&rsaPrivateKey{})] = &rsaPrivateKeyVerifier{}
	verifiers[reflect.TypeOf(&rsaPublicKey{})] = &rsaPublicKeyKeyVerifier{}

	// Set the hashers
	hashers := make(map[reflect.Type]Hasher)
	hashers[reflect.TypeOf(&bccsp.SHAOpts{})] = &hasher{hash: conf.hashFunction}
	hashers[reflect.TypeOf(&bccsp.SHA256Opts{})] = &hasher{hash: sha256.New}
	hashers[reflect.TypeOf(&bccsp.SHA384Opts{})] = &hasher{hash: sha512.New384}
	hashers[reflect.TypeOf(&bccsp.SHA3_256Opts{})] = &hasher{hash: sha3.New256}
	hashers[reflect.TypeOf(&bccsp.SHA3_384Opts{})] = &hasher{hash: sha3.New384}

	impl := &impl{
		conf:       conf,
		ks:         keyStore,
		encryptors: encryptors,
		decryptors: decryptors,
		signers:    signers,
		verifiers:  verifiers,
		hashers:    hashers}

	// Set the key generators
	keyGenerators := make(map[reflect.Type]KeyGenerator)
	keyGenerators[reflect.TypeOf(&bccsp.ECDSAKeyGenOpts{})] = &ecdsaKeyGenerator{curve: conf.ellipticCurve}
	keyGenerators[reflect.TypeOf(&bccsp.ECDSAP256KeyGenOpts{})] = &ecdsaKeyGenerator{curve: elliptic.P256()}
	keyGenerators[reflect.TypeOf(&bccsp.ECDSAP384KeyGenOpts{})] = &ecdsaKeyGenerator{curve: elliptic.P384()}
	keyGenerators[reflect.TypeOf(&bccsp.AESKeyGenOpts{})] = &aesKeyGenerator{length: conf.aesBitLength}
	keyGenerators[reflect.TypeOf(&bccsp.AES256KeyGenOpts{})] = &aesKeyGenerator{length: 32}
	keyGenerators[reflect.TypeOf(&bccsp.AES192KeyGenOpts{})] = &aesKeyGenerator{length: 24}
	keyGenerators[reflect.TypeOf(&bccsp.AES128KeyGenOpts{})] = &aesKeyGenerator{length: 16}
	keyGenerators[reflect.TypeOf(&bccsp.RSAKeyGenOpts{})] = &rsaKeyGenerator{length: conf.rsaBitLength}
	keyGenerators[reflect.TypeOf(&bccsp.RSA1024KeyGenOpts{})] = &rsaKeyGenerator{length: 1024}
	keyGenerators[reflect.TypeOf(&bccsp.RSA2048KeyGenOpts{})] = &rsaKeyGenerator{length: 2048}
	keyGenerators[reflect.TypeOf(&bccsp.RSA3072KeyGenOpts{})] = &rsaKeyGenerator{length: 3072}
	keyGenerators[reflect.TypeOf(&bccsp.RSA4096KeyGenOpts{})] = &rsaKeyGenerator{length: 4096}
	impl.keyGenerators = keyGenerators

	// Set the key generators
	keyDerivers := make(map[reflect.Type]KeyDeriver)
	keyDerivers[reflect.TypeOf(&ecdsaPrivateKey{})] = &ecdsaPrivateKeyKeyDeriver{}
	keyDerivers[reflect.TypeOf(&ecdsaPublicKey{})] = &ecdsaPublicKeyKeyDeriver{}
	keyDerivers[reflect.TypeOf(&aesPrivateKey{})] = &aesPrivateKeyKeyDeriver{bccsp: impl}
	impl.keyDerivers = keyDerivers

	return impl, nil
}

// SoftwareBasedBCCSP is the software-based implementation of the BCCSP.
type impl struct {
	conf *config
	ks   bccsp.KeyStore

	keyGenerators map[reflect.Type]KeyGenerator
	keyDerivers   map[reflect.Type]KeyDeriver
	encryptors    map[reflect.Type]Encryptor
	decryptors    map[reflect.Type]Decryptor
	signers       map[reflect.Type]Signer
	verifiers     map[reflect.Type]Verifier
	hashers       map[reflect.Type]Hasher
}

// KeyGen generates a key using opts.
func (csp *impl) KeyGen(opts bccsp.KeyGenOpts) (k bccsp.Key, err error) {
	// Validate arguments
	if opts == nil {
		return nil, errors.New("Invalid Opts parameter. It must not be nil.")
	}

	keyGenerator, found := csp.keyGenerators[reflect.TypeOf(opts)]
	if !found {
		return nil, fmt.Errorf("Unsupported 'KeyGenOpts' provided [%v]", opts)
	}

	k, err = keyGenerator.KeyGen(opts)
	if err != nil {
		return nil, err
	}

	// If the key is not Ephemeral, store it.
	if !opts.Ephemeral() {
		// Store the key
		err = csp.ks.StoreKey(k)
		if err != nil {
			return nil, fmt.Errorf("Failed storing key [%s]. [%s]", opts.Algorithm(), err)
		}
	}

	return k, nil
}

// KeyDeriv derives a key from k using opts.
// The opts argument should be appropriate for the primitive used.
func (csp *impl) KeyDeriv(k bccsp.Key, opts bccsp.KeyDerivOpts) (dk bccsp.Key, err error) {
	// Validate arguments
	if k == nil {
		return nil, errors.New("Invalid Key. It must not be nil.")
	}
	if opts == nil {
		return nil, errors.New("Invalid opts. It must not be nil.")
	}

	keyDeriver, found := csp.keyDerivers[reflect.TypeOf(k)]
	if !found {
		return nil, fmt.Errorf("Unsupported 'Key' provided [%v]", k)
	}

	k, err = keyDeriver.KeyDeriv(k, opts)
	if err != nil {
		return nil, err
	}

	// If the key is not Ephemeral, store it.
	if !opts.Ephemeral() {
		// Store the key
		err = csp.ks.StoreKey(k)
		if err != nil {
			return nil, fmt.Errorf("Failed storing key [%s]. [%s]", opts.Algorithm(), err)
		}
	}

	return k, nil
}

// KeyImport imports a key from its raw representation using opts.
// The opts argument should be appropriate for the primitive used.
func (csp *impl) KeyImport(raw interface{}, opts bccsp.KeyImportOpts) (k bccsp.Key, err error) {
	// Validate arguments
	if raw == nil {
		return nil, errors.New("Invalid raw. Cannot be nil")
	}

	if opts == nil {
		return nil, errors.New("Invalid Opts parameter. It must not be nil.")
	}

	switch opts.(type) {

	case *bccsp.AES256ImportKeyOpts:
		aesRaw, ok := raw.([]byte)
		if !ok {
			return nil, errors.New("[AES256ImportKeyOpts] Invalid raw material. Expected byte array.")
		}

		if len(aesRaw) != 32 {
			return nil, fmt.Errorf("[AES256ImportKeyOpts] Invalid Key Length [%d]. Must be 32 bytes", len(aesRaw))
		}

		aesK := &aesPrivateKey{utils.Clone(aesRaw), false}

		// If the key is not Ephemeral, store it.
		if !opts.Ephemeral() {
			// Store the key
			err = csp.ks.StoreKey(aesK)
			if err != nil {
				return nil, fmt.Errorf("Failed storing AES key [%s]", err)
			}
		}

		return aesK, nil

	case *bccsp.HMACImportKeyOpts:
		aesRaw, ok := raw.([]byte)
		if !ok {
			return nil, errors.New("[HMACImportKeyOpts] Invalid raw material. Expected byte array.")
		}

		if len(aesRaw) == 0 {
			return nil, errors.New("[HMACImportKeyOpts] Invalid raw. It must not be nil.")
		}

		aesK := &aesPrivateKey{utils.Clone(aesRaw), false}

		// If the key is not Ephemeral, store it.
		if !opts.Ephemeral() {
			// Store the key
			err = csp.ks.StoreKey(aesK)
			if err != nil {
				return nil, fmt.Errorf("Failed storing AES key [%s]", err)
			}
		}

		return aesK, nil

	case *bccsp.ECDSAPKIXPublicKeyImportOpts:
		der, ok := raw.([]byte)
		if !ok {
			return nil, errors.New("[ECDSAPKIXPublicKeyImportOpts] Invalid raw material. Expected byte array.")
		}

		if len(der) == 0 {
			return nil, errors.New("[ECDSAPKIXPublicKeyImportOpts] Invalid raw. It must not be nil.")
		}

		lowLevelKey, err := utils.DERToPublicKey(der)
		if err != nil {
			return nil, fmt.Errorf("Failed converting PKIX to ECDSA public key [%s]", err)
		}

		ecdsaPK, ok := lowLevelKey.(*ecdsa.PublicKey)
		if !ok {
			return nil, errors.New("Failed casting to ECDSA public key. Invalid raw material.")
		}

		k = &ecdsaPublicKey{ecdsaPK}

		// If the key is not Ephemeral, store it.
		if !opts.Ephemeral() {
			// Store the key
			err = csp.ks.StoreKey(k)
			if err != nil {
				return nil, fmt.Errorf("Failed storing ECDSA key [%s]", err)
			}
		}

		return k, nil

	case *bccsp.ECDSAPrivateKeyImportOpts:
		der, ok := raw.([]byte)
		if !ok {
			return nil, errors.New("[ECDSADERPrivateKeyImportOpts] Invalid raw material. Expected byte array.")
		}

		if len(der) == 0 {
			return nil, errors.New("[ECDSADERPrivateKeyImportOpts] Invalid raw. It must not be nil.")
		}

		lowLevelKey, err := utils.DERToPrivateKey(der)
		if err != nil {
			return nil, fmt.Errorf("Failed converting PKIX to ECDSA public key [%s]", err)
		}

		ecdsaSK, ok := lowLevelKey.(*ecdsa.PrivateKey)
		if !ok {
			return nil, errors.New("Failed casting to ECDSA public key. Invalid raw material.")
		}

		k = &ecdsaPrivateKey{ecdsaSK}

		// If the key is not Ephemeral, store it.
		if !opts.Ephemeral() {
			// Store the key
			err = csp.ks.StoreKey(k)
			if err != nil {
				return nil, fmt.Errorf("Failed storing ECDSA key [%s]", err)
			}
		}

		return k, nil

	case *bccsp.ECDSAGoPublicKeyImportOpts:
		lowLevelKey, ok := raw.(*ecdsa.PublicKey)
		if !ok {
			return nil, errors.New("[ECDSAGoPublicKeyImportOpts] Invalid raw material. Expected *ecdsa.PublicKey.")
		}

		k = &ecdsaPublicKey{lowLevelKey}

		// If the key is not Ephemeral, store it.
		if !opts.Ephemeral() {
			// Store the key
			err = csp.ks.StoreKey(k)
			if err != nil {
				return nil, fmt.Errorf("Failed storing ECDSA key [%s]", err)
			}
		}

		return k, nil

	case *bccsp.RSAGoPublicKeyImportOpts:
		lowLevelKey, ok := raw.(*rsa.PublicKey)
		if !ok {
			return nil, errors.New("[RSAGoPublicKeyImportOpts] Invalid raw material. Expected *rsa.PublicKey.")
		}

		k = &rsaPublicKey{lowLevelKey}

		// If the key is not Ephemeral, store it.
		if !opts.Ephemeral() {
			// Store the key
			err = csp.ks.StoreKey(k)
			if err != nil {
				return nil, fmt.Errorf("Failed storing RSA publi key [%s]", err)
			}
		}

		return k, nil

	case *bccsp.X509PublicKeyImportOpts:
		x509Cert, ok := raw.(*x509.Certificate)
		if !ok {
			return nil, errors.New("[X509PublicKeyImportOpts] Invalid raw material. Expected *x509.Certificate.")
		}

		pk := x509Cert.PublicKey

		switch pk.(type) {
		case *ecdsa.PublicKey:
			return csp.KeyImport(pk, &bccsp.ECDSAGoPublicKeyImportOpts{Temporary: opts.Ephemeral()})
		case *rsa.PublicKey:
			return csp.KeyImport(pk, &bccsp.RSAGoPublicKeyImportOpts{Temporary: opts.Ephemeral()})
		default:
			return nil, errors.New("Certificate public key type not recognized. Supported keys: [ECDSA, RSA]")
		}

	default:
		return nil, fmt.Errorf("Unsupported 'KeyImportOptions' provided [%v]", opts)
	}
}

// GetKey returns the key this CSP associates to
// the Subject Key Identifier ski.
func (csp *impl) GetKey(ski []byte) (k bccsp.Key, err error) {
	return csp.ks.GetKey(ski)
}

// Hash hashes messages msg using options opts.
func (csp *impl) Hash(msg []byte, opts bccsp.HashOpts) (digest []byte, err error) {
	// Validate arguments
	if opts == nil {
		return nil, errors.New("Invalid opts. It must not be nil.")
	}

	hasher, found := csp.hashers[reflect.TypeOf(opts)]
	if !found {
		return nil, fmt.Errorf("Unsupported 'HashOpt' provided [%v]", opts)
	}

	return hasher.Hash(msg, opts)
}

// GetHash returns and instance of hash.Hash using options opts.
// If opts is nil then the default hash function is returned.
func (csp *impl) GetHash(opts bccsp.HashOpts) (h hash.Hash, err error) {
	// Validate arguments
	if opts == nil {
		return nil, errors.New("Invalid opts. It must not be nil.")
	}

	hasher, found := csp.hashers[reflect.TypeOf(opts)]
	if !found {
		return nil, fmt.Errorf("Unsupported 'HashOpt' provided [%v]", opts)
	}

	return hasher.GetHash(opts)
}

// Sign signs digest using key k.
// The opts argument should be appropriate for the primitive used.
//
// Note that when a signature of a hash of a larger message is needed,
// the caller is responsible for hashing the larger message and passing
// the hash (as digest).
func (csp *impl) Sign(k bccsp.Key, digest []byte, opts bccsp.SignerOpts) (signature []byte, err error) {
	// Validate arguments
	if k == nil {
		return nil, errors.New("Invalid Key. It must not be nil.")
	}
	if len(digest) == 0 {
		return nil, errors.New("Invalid digest. Cannot be empty.")
	}

	signer, found := csp.signers[reflect.TypeOf(k)]
	if !found {
		return nil, fmt.Errorf("Unsupported 'SignKey' provided [%v]", k)
	}

	return signer.Sign(k, digest, opts)
}

// Verify verifies signature against key k and digest
func (csp *impl) Verify(k bccsp.Key, signature, digest []byte, opts bccsp.SignerOpts) (valid bool, err error) {
	// Validate arguments
	if k == nil {
		return false, errors.New("Invalid Key. It must not be nil.")
	}
	if len(signature) == 0 {
		return false, errors.New("Invalid signature. Cannot be empty.")
	}
	if len(digest) == 0 {
		return false, errors.New("Invalid digest. Cannot be empty.")
	}

	verifier, found := csp.verifiers[reflect.TypeOf(k)]
	if !found {
		return false, fmt.Errorf("Unsupported 'VerifyKey' provided [%v]", k)
	}

	return verifier.Verify(k, signature, digest, opts)

}

// Encrypt encrypts plaintext using key k.
// The opts argument should be appropriate for the primitive used.
func (csp *impl) Encrypt(k bccsp.Key, plaintext []byte, opts bccsp.EncrypterOpts) (ciphertext []byte, err error) {
	// Validate arguments
	if k == nil {
		return nil, errors.New("Invalid Key. It must not be nil.")
	}

	encryptor, found := csp.encryptors[reflect.TypeOf(k)]
	if !found {
		return nil, fmt.Errorf("Unsupported 'EncryptKey' provided [%v]", k)
	}

	return encryptor.Encrypt(k, plaintext, opts)
}

// Decrypt decrypts ciphertext using key k.
// The opts argument should be appropriate for the primitive used.
func (csp *impl) Decrypt(k bccsp.Key, ciphertext []byte, opts bccsp.DecrypterOpts) (plaintext []byte, err error) {
	// Validate arguments
	if k == nil {
		return nil, errors.New("Invalid Key. It must not be nil.")
	}

	decryptor, found := csp.decryptors[reflect.TypeOf(k)]
	if !found {
		return nil, fmt.Errorf("Unsupported 'DecryptKey' provided [%v]", k)
	}

	return decryptor.Decrypt(k, ciphertext, opts)
}
