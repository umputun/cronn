// Code generated by moq; DO NOT EDIT.
// github.com/matryer/moq

package mocks

import (
	"context"
	"sync"
)

// NotifierMock is a mock implementation of service.Notifier.
//
//	func TestSomethingThatUsesNotifier(t *testing.T) {
//
//		// make and configure a mocked service.Notifier
//		mockedNotifier := &NotifierMock{
//			IsOnCompletionFunc: func() bool {
//				panic("mock out the IsOnCompletion method")
//			},
//			IsOnErrorFunc: func() bool {
//				panic("mock out the IsOnError method")
//			},
//			MakeCompletionHTMLFunc: func(spec string, command string) (string, error) {
//				panic("mock out the MakeCompletionHTML method")
//			},
//			MakeErrorHTMLFunc: func(spec string, command string, errorLog string) (string, error) {
//				panic("mock out the MakeErrorHTML method")
//			},
//			SendFunc: func(ctx context.Context, subj string, text string) error {
//				panic("mock out the Send method")
//			},
//		}
//
//		// use mockedNotifier in code that requires service.Notifier
//		// and then make assertions.
//
//	}
type NotifierMock struct {
	// IsOnCompletionFunc mocks the IsOnCompletion method.
	IsOnCompletionFunc func() bool

	// IsOnErrorFunc mocks the IsOnError method.
	IsOnErrorFunc func() bool

	// MakeCompletionHTMLFunc mocks the MakeCompletionHTML method.
	MakeCompletionHTMLFunc func(spec string, command string) (string, error)

	// MakeErrorHTMLFunc mocks the MakeErrorHTML method.
	MakeErrorHTMLFunc func(spec string, command string, errorLog string) (string, error)

	// SendFunc mocks the Send method.
	SendFunc func(ctx context.Context, subj string, text string) error

	// calls tracks calls to the methods.
	calls struct {
		// IsOnCompletion holds details about calls to the IsOnCompletion method.
		IsOnCompletion []struct {
		}
		// IsOnError holds details about calls to the IsOnError method.
		IsOnError []struct {
		}
		// MakeCompletionHTML holds details about calls to the MakeCompletionHTML method.
		MakeCompletionHTML []struct {
			// Spec is the spec argument value.
			Spec string
			// Command is the command argument value.
			Command string
		}
		// MakeErrorHTML holds details about calls to the MakeErrorHTML method.
		MakeErrorHTML []struct {
			// Spec is the spec argument value.
			Spec string
			// Command is the command argument value.
			Command string
			// ErrorLog is the errorLog argument value.
			ErrorLog string
		}
		// Send holds details about calls to the Send method.
		Send []struct {
			// Ctx is the ctx argument value.
			Ctx context.Context
			// Subj is the subj argument value.
			Subj string
			// Text is the text argument value.
			Text string
		}
	}
	lockIsOnCompletion     sync.RWMutex
	lockIsOnError          sync.RWMutex
	lockMakeCompletionHTML sync.RWMutex
	lockMakeErrorHTML      sync.RWMutex
	lockSend               sync.RWMutex
}

// IsOnCompletion calls IsOnCompletionFunc.
func (mock *NotifierMock) IsOnCompletion() bool {
	if mock.IsOnCompletionFunc == nil {
		panic("NotifierMock.IsOnCompletionFunc: method is nil but Notifier.IsOnCompletion was just called")
	}
	callInfo := struct {
	}{}
	mock.lockIsOnCompletion.Lock()
	mock.calls.IsOnCompletion = append(mock.calls.IsOnCompletion, callInfo)
	mock.lockIsOnCompletion.Unlock()
	return mock.IsOnCompletionFunc()
}

// IsOnCompletionCalls gets all the calls that were made to IsOnCompletion.
// Check the length with:
//
//	len(mockedNotifier.IsOnCompletionCalls())
func (mock *NotifierMock) IsOnCompletionCalls() []struct {
} {
	var calls []struct {
	}
	mock.lockIsOnCompletion.RLock()
	calls = mock.calls.IsOnCompletion
	mock.lockIsOnCompletion.RUnlock()
	return calls
}

// IsOnError calls IsOnErrorFunc.
func (mock *NotifierMock) IsOnError() bool {
	if mock.IsOnErrorFunc == nil {
		panic("NotifierMock.IsOnErrorFunc: method is nil but Notifier.IsOnError was just called")
	}
	callInfo := struct {
	}{}
	mock.lockIsOnError.Lock()
	mock.calls.IsOnError = append(mock.calls.IsOnError, callInfo)
	mock.lockIsOnError.Unlock()
	return mock.IsOnErrorFunc()
}

// IsOnErrorCalls gets all the calls that were made to IsOnError.
// Check the length with:
//
//	len(mockedNotifier.IsOnErrorCalls())
func (mock *NotifierMock) IsOnErrorCalls() []struct {
} {
	var calls []struct {
	}
	mock.lockIsOnError.RLock()
	calls = mock.calls.IsOnError
	mock.lockIsOnError.RUnlock()
	return calls
}

// MakeCompletionHTML calls MakeCompletionHTMLFunc.
func (mock *NotifierMock) MakeCompletionHTML(spec string, command string) (string, error) {
	if mock.MakeCompletionHTMLFunc == nil {
		panic("NotifierMock.MakeCompletionHTMLFunc: method is nil but Notifier.MakeCompletionHTML was just called")
	}
	callInfo := struct {
		Spec    string
		Command string
	}{
		Spec:    spec,
		Command: command,
	}
	mock.lockMakeCompletionHTML.Lock()
	mock.calls.MakeCompletionHTML = append(mock.calls.MakeCompletionHTML, callInfo)
	mock.lockMakeCompletionHTML.Unlock()
	return mock.MakeCompletionHTMLFunc(spec, command)
}

// MakeCompletionHTMLCalls gets all the calls that were made to MakeCompletionHTML.
// Check the length with:
//
//	len(mockedNotifier.MakeCompletionHTMLCalls())
func (mock *NotifierMock) MakeCompletionHTMLCalls() []struct {
	Spec    string
	Command string
} {
	var calls []struct {
		Spec    string
		Command string
	}
	mock.lockMakeCompletionHTML.RLock()
	calls = mock.calls.MakeCompletionHTML
	mock.lockMakeCompletionHTML.RUnlock()
	return calls
}

// MakeErrorHTML calls MakeErrorHTMLFunc.
func (mock *NotifierMock) MakeErrorHTML(spec string, command string, errorLog string) (string, error) {
	if mock.MakeErrorHTMLFunc == nil {
		panic("NotifierMock.MakeErrorHTMLFunc: method is nil but Notifier.MakeErrorHTML was just called")
	}
	callInfo := struct {
		Spec     string
		Command  string
		ErrorLog string
	}{
		Spec:     spec,
		Command:  command,
		ErrorLog: errorLog,
	}
	mock.lockMakeErrorHTML.Lock()
	mock.calls.MakeErrorHTML = append(mock.calls.MakeErrorHTML, callInfo)
	mock.lockMakeErrorHTML.Unlock()
	return mock.MakeErrorHTMLFunc(spec, command, errorLog)
}

// MakeErrorHTMLCalls gets all the calls that were made to MakeErrorHTML.
// Check the length with:
//
//	len(mockedNotifier.MakeErrorHTMLCalls())
func (mock *NotifierMock) MakeErrorHTMLCalls() []struct {
	Spec     string
	Command  string
	ErrorLog string
} {
	var calls []struct {
		Spec     string
		Command  string
		ErrorLog string
	}
	mock.lockMakeErrorHTML.RLock()
	calls = mock.calls.MakeErrorHTML
	mock.lockMakeErrorHTML.RUnlock()
	return calls
}

// Send calls SendFunc.
func (mock *NotifierMock) Send(ctx context.Context, subj string, text string) error {
	if mock.SendFunc == nil {
		panic("NotifierMock.SendFunc: method is nil but Notifier.Send was just called")
	}
	callInfo := struct {
		Ctx  context.Context
		Subj string
		Text string
	}{
		Ctx:  ctx,
		Subj: subj,
		Text: text,
	}
	mock.lockSend.Lock()
	mock.calls.Send = append(mock.calls.Send, callInfo)
	mock.lockSend.Unlock()
	return mock.SendFunc(ctx, subj, text)
}

// SendCalls gets all the calls that were made to Send.
// Check the length with:
//
//	len(mockedNotifier.SendCalls())
func (mock *NotifierMock) SendCalls() []struct {
	Ctx  context.Context
	Subj string
	Text string
} {
	var calls []struct {
		Ctx  context.Context
		Subj string
		Text string
	}
	mock.lockSend.RLock()
	calls = mock.calls.Send
	mock.lockSend.RUnlock()
	return calls
}
