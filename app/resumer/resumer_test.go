package resumer

import (
	"io/ioutil"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResumer_OnStart(t *testing.T) {
	r := New("/tmp/resumer.test", true)
	defer os.RemoveAll("/tmp/resumer.test")

	s, err := r.OnStart("cmd 1 2 blah")
	assert.Nil(t, err)
	t.Log(s)

	data, err := ioutil.ReadFile(s) // nolint gosec
	assert.Nil(t, err)
	assert.Equal(t, "cmd 1 2 blah", string(data))
}

func TestResumer_OnFinish(t *testing.T) {
	r := New("/tmp/resumer.test", true)
	defer os.RemoveAll("/tmp/resumer.test")

	s, err := r.OnStart("cmd 1 2 blah")
	assert.Nil(t, err)
	err = r.OnFinish(s)

	assert.Nil(t, err)
	_, err = ioutil.ReadFile(s) // nolint gosec
	assert.NotNil(t, err)
}

func TestResumer_List(t *testing.T) {
	r := New("/tmp/resumer.test", true)
	defer os.RemoveAll("/tmp/resumer.test")

	_, e := r.OnStart("cmd1 1 2 blah")
	assert.Nil(t, e)
	_, e = r.OnStart("cmd2 1 2 3 blah")
	assert.Nil(t, e)
	_, e = r.OnStart("cmd3 blah")
	assert.Nil(t, e)

	err := ioutil.WriteFile("/tmp/resumer.test/old.cronn", []byte("something"), 0600) //nolint
	require.NoError(t, err)
	defer os.Remove("/tmp/resumer.test/old.cronn")

	res := r.List()
	assert.Equal(t, 4, len(res))

	_ = os.Chtimes("/tmp/resumer.test/old.cronn",
		time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC))
	res = r.List()
	assert.Equal(t, 3, len(res))

	r.enabled = false
	res = r.List()
	assert.Equal(t, 0, len(res))

}
