package resumer

import (
	"io/ioutil"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestResumer_OnStart(t *testing.T) {
	os.RemoveAll("/tmp/resumer.test")
	r := New("/tmp/resumer.test", true)

	s, err := r.OnStart("cmd 1 2 blah")
	assert.Nil(t, err)
	t.Log(s)

	data, err := ioutil.ReadFile(s)
	assert.Nil(t, err)
	assert.Equal(t, "cmd 1 2 blah", string(data))
}

func TestResumer_OnFinish(t *testing.T) {
	os.RemoveAll("/tmp/resumer.test")
	r := New("/tmp/resumer.test", true)

	s, err := r.OnStart("cmd 1 2 blah")
	assert.Nil(t, err)
	err = r.OnFinish(s)

	assert.Nil(t, err)
	_, err = ioutil.ReadFile(s)
	assert.NotNil(t, err)
}

func TestResumer_List(t *testing.T) {

	os.RemoveAll("/tmp/resumer.test")
	r := New("/tmp/resumer.test", true)

	_, e := r.OnStart("cmd1 1 2 blah")
	assert.Nil(t, e)
	_, e = r.OnStart("cmd2 1 2 3 blah")
	assert.Nil(t, e)
	_, e = r.OnStart("cmd3 blah")
	assert.Nil(t, e)

	res := r.List()
	assert.Equal(t, 3, len(res))
}
