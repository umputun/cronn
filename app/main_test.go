package main

import (
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/natefinch/lumberjack.v2"
	"io/ioutil"
	"os"
	"syscall"
	"testing"
)

func Test_makeHostName(t *testing.T) {
	opts.Notify.HostName = "test"
	assert.Equal(t, "test", makeHostName())

	opts.Notify.HostName = ""
	exp, err := os.Hostname()
	require.NoError(t, err)
	assert.Equal(t, exp, makeHostName())
}

func Test_makeNotifier(t *testing.T) {
	opts.Notify.EnabledCompletion, opts.Notify.EnabledError = false, false
	assert.Nil(t, makeNotifier())

	opts.Notify.EnabledCompletion = true
	notif := makeNotifier()
	require.NotNil(t, notif)
	assert.Equal(t, "cronn@"+makeHostName(), notif.From)
}

func Test_setupLogsWithLogsDisabled(t *testing.T) {
	opts.Log.Enabled = false
	assert.Equal(t, os.Stdout, setupLogs())
}

func Test_setupLogsToFile(t *testing.T) {
	tmpfile, err := ioutil.TempFile("", "")
	require.NoError(t, err)

	opts.Log.Enabled = true
	opts.Log.Filename = tmpfile.Name()
	opts.Log.MaxSize = 100
	opts.Log.MaxBackups = 7
	opts.Log.MaxAge = 0
	opts.Log.EnabledCompress = false

	out := setupLogs()
	assert.IsType(t, &lumberjack.Logger{}, out)

	logger := out.(*lumberjack.Logger)
	assert.Equal(t, tmpfile.Name(), logger.Filename)
	assert.Equal(t, 100, logger.MaxSize)
	assert.Equal(t, 7, logger.MaxBackups)
	assert.Equal(t, 0, logger.MaxAge)
	assert.Equal(t, false, logger.Compress)
}

func Test_signals(t *testing.T) {
	sysCalls := []syscall.Signal{syscall.SIGQUIT, syscall.SIGHUP, syscall.SIGTERM}
	os.Args = []string{"-c @every 2s echo {{.UNIX}}"}

	for _, signal := range sysCalls {
		done := make(chan struct{})
		finished := make(chan struct{})
		go func() {
			main()
			close(done)
		}()

		go func(s syscall.Signal) {
			<-done
			e := syscall.Kill(syscall.Getpid(), s)
			require.NoError(t, e)
			close(finished)
		}(signal)

		<-finished
	}
}
