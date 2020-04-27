package nomad

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"testing"
	"time"

	"github.com/hashicorp/go-licensing"
	msgpackrpc "github.com/hashicorp/net-rpc-msgpackrpc"
	nomadLicense "github.com/hashicorp/nomad-licensing/license"
	"github.com/hashicorp/nomad/acl"
	"github.com/hashicorp/nomad/nomad/mock"
	"github.com/hashicorp/nomad/nomad/structs"
	"github.com/hashicorp/nomad/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLicenseEndpoint_GetLicense(t *testing.T) {
	assert := assert.New(t)
	t.Parallel()
	s1, cleanupS1 := TestServer(t, nil)
	defer cleanupS1()
	codec := rpcClient(t, s1)
	testutil.WaitForLeader(t, s1.RPC)

	l := mock.StoredLicense()
	require.NoError(t, s1.fsm.State().UpsertLicense(1001, l))

	get := &structs.LicenseGetRequest{
		QueryOptions: structs.QueryOptions{Region: "global"},
	}
	var resp structs.LicenseGetResponse
	require.NoError(t, msgpackrpc.CallWithCodec(codec, "License.GetLicense", get, &resp))
	assert.EqualValues(uint64(1001), resp.Index)
	assert.Equal(l, resp.License)
}

func TestLicenseEndpoint_UpsertLicense(t *testing.T) {
	assert := assert.New(t)
	t.Parallel()

	// Generate key pair
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)

	// Encoded public key
	pubKey := base64.StdEncoding.EncodeToString(pub)

	now := time.Now()
	exp := 1 * time.Hour
	// Create an initial temporary license
	newLicense := &licensing.License{
		LicenseID:       "temporary-license",
		CustomerID:      "temporary license customer",
		InstallationID:  "*",
		Product:         nomadLicense.ProductName,
		IssueTime:       now,
		StartTime:       now,
		ExpirationTime:  now.Add(exp),
		TerminationTime: now.Add(exp),
		Flags:           nil,
	}

	// Sign the license
	signedTemp, err := newLicense.SignedString(priv)
	require.NoError(t, err)

	// Callback to configure license watcher
	cb := func(cfg *Config) {
		cfg.LicenseConfig = &licensing.WatcherOptions{
			ProductName:          nomadLicense.ProductName,
			InitLicense:          signedTemp,
			AdditionalPublicKeys: []string{pubKey},
		}
	}

	s1, cleanupS1 := TestServer(t, cb)
	defer cleanupS1()
	codec := rpcClient(t, s1)
	testutil.WaitForLeader(t, s1.RPC)

	// Create a new license to upsert
	putLicense := &licensing.License{
		LicenseID:       "new-temp-license",
		CustomerID:      "temporary license customer",
		InstallationID:  "*",
		Product:         nomadLicense.ProductName,
		IssueTime:       now,
		StartTime:       now,
		ExpirationTime:  now.Add(exp),
		TerminationTime: now.Add(exp),
		Flags:           nil,
	}

	putSigned, err := putLicense.SignedString(priv)
	require.NoError(t, err)

	req := &structs.LicenseUpsertRequest{
		License:      &structs.StoredLicense{Signed: putSigned},
		WriteRequest: structs.WriteRequest{Region: "global"},
	}
	var resp structs.GenericResponse
	require.NoError(t, msgpackrpc.CallWithCodec(codec, "License.UpsertLicense", req, &resp))
	assert.NotEqual(uint64(0), resp.Index)

	// Check we created the license
	out, err := s1.fsm.State().License(nil)
	require.NoError(t, err)
	assert.Equal(out.Signed, putSigned)
}

func TestLicenseEndpoint_UpsertLicenses_ACL(t *testing.T) {
	assert := assert.New(t)
	t.Parallel()
	s1, root, cleanupS1 := TestACLServer(t, nil)
	defer cleanupS1()
	codec := rpcClient(t, s1)
	testutil.WaitForLeader(t, s1.RPC)

	l := mock.StoredLicense()
	state := s1.fsm.State()

	// Create the token
	invalidToken := mock.CreateToken(t, state, 1003, []string{"test-invalid", acl.PolicyWrite})

	// Create the register request
	req := &structs.LicenseUpsertRequest{
		License:      l,
		WriteRequest: structs.WriteRequest{Region: "global"},
	}

	// Upsert the license without a token and expect failure
	{
		var resp structs.GenericResponse
		err := msgpackrpc.CallWithCodec(codec, "License.UpsertLicense", req, &resp)
		assert.NotNil(err)
		assert.Equal(err.Error(), structs.ErrPermissionDenied.Error())

		// Check we did not create the namespaces
		out, err := s1.fsm.State().License(nil)
		require.NoError(t, err)
		assert.Nil(out)
	}

	// Try with an invalid token
	req.AuthToken = invalidToken.SecretID
	{
		var resp structs.GenericResponse
		err := msgpackrpc.CallWithCodec(codec, "License.UpsertLicense", req, &resp)
		assert.NotNil(err)
		assert.Equal(err.Error(), structs.ErrPermissionDenied.Error())

		// Check we did not create the namespaces
		out, err := s1.fsm.State().License(nil)
		assert.Nil(err)
		assert.Nil(out)

	}

	// Try with a root token
	req.AuthToken = root.SecretID
	{
		var resp structs.GenericResponse
		assert.Nil(msgpackrpc.CallWithCodec(codec, "License.UpsertLicense", req, &resp))
		assert.NotEqual(uint64(0), resp.Index)

		// Check we created the namespaces
		out, err := s1.fsm.State().License(nil)
		require.NoError(t, err)
		assert.NotNil(out)
	}
}
