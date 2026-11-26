package storage

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRequireServerVersion(t *testing.T) {
	cases := []struct {
		versionNum int
		wantErr    bool
	}{
		{versionNum: 170000, wantErr: false},
		{versionNum: 170012, wantErr: false},
		{versionNum: 180001, wantErr: false},
		{versionNum: 160009, wantErr: true},
		{versionNum: 0, wantErr: true},
	}

	for _, tc := range cases {
		err := requireServerVersion(tc.versionNum)
		if tc.wantErr {
			require.Error(t, err, "version_num %d", tc.versionNum)
			assert.Contains(t, err.Error(), "PostgreSQL 17 or newer is required")
			continue
		}
		require.NoError(t, err, "version_num %d", tc.versionNum)
	}
}
