// build +ent

package nomad

import (
	"context"
	"encoding/base64"
	"fmt"
	"testing"
	"time"

	"github.com/hashicorp/nomad-licensing/license"
	nomadLicense "github.com/hashicorp/nomad-licensing/license"
	"github.com/hashicorp/nomad/helper/uuid"
	"github.com/hashicorp/nomad/nomad/structs"
	"github.com/hashicorp/nomad/testutil"

	"github.com/stretchr/testify/require"
)

func previousID(t *testing.T, lw *LicenseWatcher) string {
	return lw.License().LicenseID
}

func TestLicenseWatcher_UpdatingWatcher(t *testing.T) {
	t.Parallel()

	s1, cleanupS1 := TestServer(t, func(c *Config) {
		c.LicenseConfig = &LicenseConfig{
			AdditionalPubKeys: []string{base64.StdEncoding.EncodeToString(nomadLicense.TestPublicKey)},
		}
	})
	defer cleanupS1()
	state := s1.State()
	lw := s1.EnterpriseState.licenseWatcher

	initLicense, _ := lw.watcher.License()
	newLicense := license.NewTestLicense(license.TestGovernancePolicyFlags())
	stored := &structs.StoredLicense{
		Signed:      newLicense.Signed,
		CreateIndex: uint64(1000),
	}
	previousID := previousID(t, lw)
	state.UpsertLicense(1000, stored)
	waitForLicense(t, lw, previousID)

	fetchedLicense, err := lw.watcher.License()

	require.NoError(t, err)
	require.False(t, fetchedLicense.Equal(initLicense), "fetched license should be different from the inital")
	require.True(t, fetchedLicense.Equal(newLicense.License.License), fmt.Sprintf("got: %s wanted: %s", fetchedLicense, newLicense.License.License))
}

func TestLicenseWatcher_UpdateCh(t *testing.T) {
	t.Parallel()

	s1, cleanupS1 := TestServer(t, func(c *Config) {
		c.LicenseConfig = &LicenseConfig{
			AdditionalPubKeys: []string{base64.StdEncoding.EncodeToString(nomadLicense.TestPublicKey)},
		}
	})
	defer cleanupS1()
	state := s1.State()
	lw := s1.EnterpriseState.licenseWatcher

	newLicense := license.NewTestLicense(temporaryFlags())
	stored := &structs.StoredLicense{
		Signed:      newLicense.Signed,
		CreateIndex: uint64(1000),
	}
	previousID := previousID(t, lw)
	state.UpsertLicense(1000, stored)
	waitForLicense(t, lw, previousID)

	require.NotEqual(t, uint64(lw.License().Features), uint64(0))
	require.True(t, lw.hasFeature(license.FeatureAuditLogging))
	require.True(t, lw.hasFeature(license.FeatureMultiregionDeployments))
}

func TestLicenseWatcher_Validate(t *testing.T) {
	t.Parallel()

	s1, cleanupS1 := TestServer(t, func(c *Config) {
		c.LicenseConfig = &LicenseConfig{
			AdditionalPubKeys: []string{base64.StdEncoding.EncodeToString(nomadLicense.TestPublicKey)},
		}
	})
	defer cleanupS1()
	lw := s1.EnterpriseState.licenseWatcher

	invalidFlags := map[string]interface{}{
		"modules": []interface{}{"invalid"},
	}
	newLicense := license.NewTestLicense(invalidFlags)

	// It can be a valid go-licensing license
	_, err := lw.watcher.ValidateLicense(newLicense.Signed)
	require.NoError(t, err)

	// Ensure it is not a valid nomad license
	lic, err := lw.ValidateLicense(newLicense.Signed)
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid module")
	require.Nil(t, lic)
}

func TestLicenseWatcher_UpdateCh_Platform(t *testing.T) {
	t.Parallel()

	s1, cleanupS1 := TestServer(t, func(c *Config) {
		c.LicenseConfig = &LicenseConfig{
			AdditionalPubKeys: []string{base64.StdEncoding.EncodeToString(nomadLicense.TestPublicKey)},
		}
	})
	defer cleanupS1()
	state := s1.State()
	lw := s1.EnterpriseState.licenseWatcher

	newLicense := license.NewTestLicense(license.TestPlatformFlags())
	stored := &structs.StoredLicense{
		Signed:      newLicense.Signed,
		CreateIndex: uint64(1000),
	}
	previousID := previousID(t, lw)

	state.UpsertLicense(1000, stored)
	waitForLicense(t, lw, previousID)

	require.NotEqual(t, uint64(lw.License().Features), uint64(0))
	require.False(t, lw.hasFeature(license.FeatureAuditLogging))
	require.True(t, lw.hasFeature(license.FeatureReadScalability))
}

func waitForLicense(t *testing.T, lw *LicenseWatcher, previousID string) {
	testutil.WaitForResult(func() (bool, error) {
		l := lw.License()
		if l.LicenseID == previousID {
			return false, fmt.Errorf("expected updated license")
		}
		return true, nil
	}, func(err error) {
		require.FailNow(t, err.Error())
	})
}

func TestLicenseWatcher_FeatureCheck(t *testing.T) {
	cases := []struct {
		desc            string
		licenseFeatures []string
		f               license.Features
		has             bool
	}{
		{
			desc:            "contains feature",
			licenseFeatures: []string{license.FeatureAuditLogging.String(), license.FeatureNamespaces.String()},
			f:               license.FeatureAuditLogging,
			has:             true,
		},
		{
			desc:            "missing feature",
			licenseFeatures: []string{license.FeatureAuditLogging.String(), license.FeatureNamespaces.String()},
			f:               license.FeatureMultiregionDeployments,
			has:             false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.desc, func(t *testing.T) {
			// start watcher
			s1, cleanupS1 := TestServer(t, func(c *Config) {
				c.LicenseConfig = &LicenseConfig{
					AdditionalPubKeys: []string{base64.StdEncoding.EncodeToString(nomadLicense.TestPublicKey)},
				}
			})
			defer cleanupS1()
			state := s1.State()
			lw := s1.EnterpriseState.licenseWatcher

			ctx, cancel := context.WithCancel(context.Background())
			lw.start(ctx, state)
			defer cancel()

			flags := map[string]interface{}{
				"features": map[string]interface{}{
					"add": tc.licenseFeatures,
				},
			}

			previousID := previousID(t, lw)
			newLicense := license.NewTestLicense(flags)

			stored := &structs.StoredLicense{
				Signed:      newLicense.Signed,
				CreateIndex: uint64(1000),
			}
			state.UpsertLicense(1000, stored)
			waitForLicense(t, lw, previousID)

			require.Equal(t, tc.has, lw.hasFeature(tc.f))
		})
	}
}

func TestLicenseWatcher_PeriodicLogging(t *testing.T) {
	s1, cleanupS1 := TestServer(t, func(c *Config) {
		c.LicenseConfig = &LicenseConfig{
			AdditionalPubKeys: []string{base64.StdEncoding.EncodeToString(nomadLicense.TestPublicKey)},
		}
	})
	defer cleanupS1()
	state := s1.State()
	lw := s1.EnterpriseState.licenseWatcher

	// Create license without any added features
	flags := map[string]interface{}{}

	previousID := previousID(t, lw)
	newLicense := license.NewTestLicense(flags)

	stored := &structs.StoredLicense{
		Signed:      newLicense.Signed,
		CreateIndex: uint64(1000),
	}
	state.UpsertLicense(1000, stored)
	waitForLicense(t, lw, previousID)

	require.Error(t, lw.FeatureCheck(license.FeatureMultiregionDeployments, true))
	require.Len(t, lw.logTimes, 1)
	t1 := lw.logTimes[license.FeatureMultiregionDeployments]
	require.NotNil(t, t1)

	// Fire another feature check
	require.Error(t, lw.FeatureCheck(license.FeatureMultiregionDeployments, true))

	t2 := lw.logTimes[license.FeatureMultiregionDeployments]
	require.NotNil(t, t2)

	require.Equal(t, t1, t2)
}

func TestLicenseWatcher_ExpiredLicense(t *testing.T) {
	t.Parallel()

	s1, cleanupS1 := TestServer(t, func(c *Config) {
		c.BootstrapExpect = 1
		c.LicenseConfig = &LicenseConfig{
			AdditionalPubKeys: []string{base64.StdEncoding.EncodeToString(nomadLicense.TestPublicKey)},
		}
	})
	defer cleanupS1()
	testutil.WaitForLeader(t, s1.RPC)
	state := s1.State()
	lw := s1.EnterpriseState.licenseWatcher

	// Set expiration time
	newLicense := license.NewTestLicense(license.TestGovernancePolicyFlags())
	newLicense.License.ExpirationTime = time.Now().Add(2 * time.Second)
	newLicense.License.TerminationTime = time.Now().Add(2 * time.Second)
	signed, err := newLicense.License.License.SignedString(newLicense.TestPrivateKey)

	require.NoError(t, err)
	newLicense.Signed = signed

	stored := &structs.StoredLicense{
		Signed:      newLicense.Signed,
		CreateIndex: uint64(1000),
	}
	previousID := previousID(t, lw)
	state.UpsertLicense(1000, stored)
	waitForLicense(t, lw, previousID)

	testutil.WaitForResult(func() (bool, error) {
		if lw.Features() == license.FeatureNone {
			return true, nil
		}
		return false, fmt.Errorf("expected no features on expired license")
	}, func(err error) {
		require.FailNow(t, err.Error())
	})
}

// TestLicenseWatcher_InitLicense ensures the startup license is temporary
func TestLicenseWatcher_InitLicense(t *testing.T) {
	t.Parallel()

	s1, cleanupS1 := TestServer(t, func(c *Config) {
		c.LicenseConfig = &LicenseConfig{
			AdditionalPubKeys: []string{base64.StdEncoding.EncodeToString(nomadLicense.TestPublicKey)},
		}
	})
	defer cleanupS1()
	state := s1.State()
	lw := s1.EnterpriseState.licenseWatcher

	ctx, cancel := context.WithCancel(context.Background())
	lw.start(ctx, state)
	defer cancel()

	require.True(t, lw.License().Temporary)
}

// TestLicenseWatcher_InitLicense tests that an existing raft license is
// loaded during startup
func TestLicenseWatcher_Init_LoadRaft(t *testing.T) {
	t.Parallel()
	s1, cleanupS1 := TestServer(t, func(c *Config) {
		c.LicenseConfig = &LicenseConfig{
			AdditionalPubKeys: []string{base64.StdEncoding.EncodeToString(nomadLicense.TestPublicKey)},
			preventStart:      true,
		}
	})
	defer cleanupS1()
	state := s1.State()
	lw := s1.EnterpriseState.licenseWatcher

	// Set the tmp license create time in the past
	err := state.TmpLicenseSetMeta(10, &structs.TmpLicenseMeta{
		CreateTime: time.Now().Add(-24 * time.Hour).UnixNano()})
	require.NoError(t, err)

	// Set expiration time and re-sign
	existingLicense := license.NewTestLicense(license.TestGovernancePolicyFlags())
	existingLicense.License.CustomerID = "valid-license"
	signed, err := existingLicense.License.License.SignedString(existingLicense.TestPrivateKey)

	require.NoError(t, err)
	existingLicense.Signed = signed

	require.False(t, existingLicense.License.Temporary)

	// Store the valid license in raft
	stored := &structs.StoredLicense{
		Signed:      existingLicense.Signed,
		CreateIndex: uint64(1000),
	}
	require.NoError(t, state.UpsertLicense(1000, stored))

	prev := lw.License().LicenseID
	// start license watcher
	lw.start(s1.shutdownCtx, state)

	waitForLicense(t, lw, prev)
	// Ensure we have the license from raft
	require.False(t, lw.License().Temporary)
	require.Equal(t, "valid-license", lw.License().CustomerID)
}

// TestLicenseWatcher_Init_ExpiredTemp_Shutdown asserts that
// if a cluster is too old for a temporary license, and a valid one
// isn't applied, the agent shuts down
func TestLicenseWatcher_Init_ExpiredTemp_Shutdown(t *testing.T) {
	t.Parallel()
	s1, cleanupS1 := TestServer(t, func(c *Config) {
		c.LicenseConfig = &LicenseConfig{
			AdditionalPubKeys: []string{base64.StdEncoding.EncodeToString(nomadLicense.TestPublicKey)},
			preventStart:      true,
		}
	})
	defer cleanupS1()
	state := s1.State()
	lw := s1.EnterpriseState.licenseWatcher

	executed := make(chan struct{})
	shutdown := func() error { close(executed); return nil }

	lw.shutdownCallback = shutdown
	lw.expiredTmpGrace = 50 * time.Millisecond

	// Set the tmp license create time in the past
	err := state.TmpLicenseSetMeta(10, &structs.TmpLicenseMeta{
		CreateTime: time.Now().Add(-24 * time.Hour).UnixNano()})
	require.NoError(t, err)

	lw.start(s1.shutdownCtx, state)

	select {
	case <-executed:
		// shutdown properly
	case <-time.After(2 * time.Second):
		require.Fail(t, "timeout waiting for shutdown")
	}
}

// TestLicenseWatcher_Init_ExpiredTemp_ShutdownCancelled asserts that
// if a cluster is too old for a temporary license, and a valid one
// is applied, the agent does not shutdown
func TestLicenseWatcher_Init_ExpiredTemp_Shutdown_Cancelled(t *testing.T) {
	t.Parallel()

	s1, cleanupS1 := TestServer(t, func(c *Config) {
		c.LicenseConfig = &LicenseConfig{
			AdditionalPubKeys: []string{base64.StdEncoding.EncodeToString(nomadLicense.TestPublicKey)},
			preventStart:      true,
		}
	})
	defer cleanupS1()
	state := s1.State()
	lw := s1.EnterpriseState.licenseWatcher

	executed := make(chan struct{})
	shutdown := func() error { close(executed); return nil }

	lw.shutdownCallback = shutdown
	lw.expiredTmpGrace = 2 * time.Second

	// Set the tmp license create time in the past
	err := state.TmpLicenseSetMeta(10, &structs.TmpLicenseMeta{
		CreateTime: time.Now().Add(-24 * time.Hour).UnixNano()})
	require.NoError(t, err)

	lw.start(s1.shutdownCtx, state)

	// apply a new license
	newLicense := license.NewTestLicense(license.TestGovernancePolicyFlags())
	newLicense.License.ExpirationTime = time.Now().Add(10 * time.Minute)
	newLicense.License.TerminationTime = time.Now().Add(10 * time.Minute)
	newLicense.License.LicenseID = uuid.Generate()
	newSigned, err := newLicense.License.License.SignedString(newLicense.TestPrivateKey)
	require.NoError(t, err)

	newLicense.Signed = newSigned

	stored := &structs.StoredLicense{
		Signed:      newLicense.Signed,
		CreateIndex: uint64(1001),
	}
	state.UpsertLicense(1001, stored)

	success := make(chan struct{})
	go func() {
		select {
		case <-lw.monitorExpTmpCtx.Done():
			close(success)
			// properly avoided shutdown
		case <-executed:
			require.Fail(t, "expected shutddown to be prevented")
		}
	}()

	select {
	case <-success:
		// success
	case <-time.After(5 * time.Second):
		require.Fail(t, "timeout waiting for shutdown")
	}
}

// TestTestLicenseWatcher_Init_ExpiredValid_License tests the behavior of loading
// an expired license from raft. go-licensing will reject the update, leaving
// the temporary license in place.
func TestLicenseWatcher_Init_ExpiredValid_License(t *testing.T) {
	t.Parallel()
	s1, cleanupS1 := TestServer(t, func(c *Config) {
		c.LicenseConfig = &LicenseConfig{
			AdditionalPubKeys: []string{base64.StdEncoding.EncodeToString(nomadLicense.TestPublicKey)},
			preventStart:      true,
		}
	})
	defer cleanupS1()
	state := s1.State()
	lw := s1.EnterpriseState.licenseWatcher

	lw.start(s1.shutdownCtx, state)

	// Set expiration time
	existingLicense := license.NewTestLicense(license.TestGovernancePolicyFlags())
	existingLicense.License.ExpirationTime = time.Now().Add(-10 * time.Minute)
	existingLicense.License.TerminationTime = time.Now().Add(-10 * time.Minute)
	signed, err := existingLicense.License.License.SignedString(existingLicense.TestPrivateKey)

	require.NoError(t, err)
	existingLicense.Signed = signed

	stored := &structs.StoredLicense{
		Signed:      existingLicense.Signed,
		CreateIndex: uint64(1000),
	}
	prevID := previousID(t, lw)
	state.UpsertLicense(1000, stored)

	// TODO: (drew) go-licensing support setting expired licenses
	// The existingLicense will not be set since it is expired
	require.Equal(t, lw.License().LicenseID, "temporary-license")

	// Test that we can apply a new valid license

	// Set expiration time
	newLicense := license.NewTestLicense(license.TestGovernancePolicyFlags())
	newLicense.License.ExpirationTime = time.Now().Add(10 * time.Minute)
	newLicense.License.TerminationTime = time.Now().Add(10 * time.Minute)
	newLicense.License.LicenseID = uuid.Generate()
	newSigned, err := newLicense.License.License.SignedString(newLicense.TestPrivateKey)
	require.NoError(t, err)

	newLicense.Signed = newSigned

	stored = &structs.StoredLicense{
		Signed:      newLicense.Signed,
		CreateIndex: uint64(1001),
	}
	prevID = previousID(t, lw)
	state.UpsertLicense(1001, stored)
	waitForLicense(t, lw, prevID)

	require.Equal(t, newLicense.License.LicenseID, lw.License().LicenseID)
	require.True(t, lw.hasFeature(license.FeatureSentinelPolicies))
}

func TestTempLicenseTooOld(t *testing.T) {
	c1 := time.Now()
	require.False(t, tempLicenseTooOld(c1))

	// cluster 10 min old
	c2 := time.Now().Add(-10 * time.Minute)
	require.False(t, tempLicenseTooOld(c2))

	// cluster near expired
	c3 := time.Now().Add(-temporaryLicenseTimeLimit).Add(1 * time.Minute)
	require.False(t, tempLicenseTooOld(c3))

	// cluster too old
	c4 := time.Now().Add(-24 * time.Hour)
	require.True(t, tempLicenseTooOld(c4))
}

func TestTempLicense_Cluster_LicenseMeta(t *testing.T) {
	t.Parallel()

	s1, cleanupS1 := TestServer(t, func(c *Config) {
		c.BootstrapExpect = 3
	})
	defer cleanupS1()
	lw1 := s1.EnterpriseState.licenseWatcher

	s1s := s1.State()

	s2, cleanupS2 := TestServer(t, func(c *Config) {
		c.BootstrapExpect = 3
	})
	defer cleanupS2()
	s2s := s2.State()
	lw2 := s2.EnterpriseState.licenseWatcher

	s3, cleanupS3 := TestServer(t, func(c *Config) {
		c.BootstrapExpect = 3
	})
	defer cleanupS3()
	s3s := s3.State()
	lw3 := s3.EnterpriseState.licenseWatcher

	// No servers should have tmp metadata before leadership
	// Servers should have features / temporary license while waiting
	s1Meta, err := s1s.TmpLicenseMeta(nil)
	require.NoError(t, err)
	require.Nil(t, s1Meta)
	require.True(t, lw1.hasFeature(license.FeatureAuditLogging))

	s2Meta, err := s2s.TmpLicenseMeta(nil)
	require.NoError(t, err)
	require.Nil(t, s2Meta)
	require.True(t, lw2.hasFeature(license.FeatureAuditLogging))

	s3Meta, err := s3s.TmpLicenseMeta(nil)
	require.NoError(t, err)
	require.Nil(t, s3Meta)
	require.True(t, lw3.hasFeature(license.FeatureAuditLogging))

	// Join servers and establish leadership
	TestJoin(t, s1, s2, s3)
	testutil.WaitForLeader(t, s1.RPC)

	var t1, t2, t3 int64
	testutil.WaitForResult(func() (bool, error) {
		meta, err := s1.State().TmpLicenseMeta(nil)
		require.NoError(t, err)

		if meta == nil {
			return false, fmt.Errorf("expected tmp license meta")
		}
		t1 = meta.CreateTime
		return true, nil
	}, func(err error) {
		require.FailNow(t, err.Error())
	})

	testutil.WaitForResult(func() (bool, error) {
		meta, err := s2.State().TmpLicenseMeta(nil)
		require.NoError(t, err)

		if meta == nil {
			return false, fmt.Errorf("expected tmp license meta")
		}
		t2 = meta.CreateTime
		return true, nil
	}, func(err error) {
		require.FailNow(t, err.Error())
	})

	testutil.WaitForResult(func() (bool, error) {
		meta, err := s3.State().TmpLicenseMeta(nil)
		require.NoError(t, err)

		if meta == nil {
			return false, fmt.Errorf("expected tmp license meta")
		}
		t3 = meta.CreateTime
		return true, nil
	}, func(err error) {
		require.FailNow(t, err.Error())
	})

	require.Equal(t, t1, t2)
	require.Equal(t, t2, t3)
}