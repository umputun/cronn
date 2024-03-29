// Code generated by moq; DO NOT EDIT.
// github.com/matryer/moq

package mocks

import (
	"sync"
)

// DedupperMock is a mock implementation of service.Dedupper.
//
// 	func TestSomethingThatUsesDedupper(t *testing.T) {
//
// 		// make and configure a mocked service.Dedupper
// 		mockedDedupper := &DedupperMock{
// 			AddFunc: func(key string) bool {
// 				panic("mock out the Add method")
// 			},
// 			RemoveFunc: func(key string)  {
// 				panic("mock out the Remove method")
// 			},
// 		}
//
// 		// use mockedDedupper in code that requires service.Dedupper
// 		// and then make assertions.
//
// 	}
type DedupperMock struct {
	// AddFunc mocks the Add method.
	AddFunc func(key string) bool

	// RemoveFunc mocks the Remove method.
	RemoveFunc func(key string)

	// calls tracks calls to the methods.
	calls struct {
		// Add holds details about calls to the Add method.
		Add []struct {
			// Key is the key argument value.
			Key string
		}
		// Remove holds details about calls to the Remove method.
		Remove []struct {
			// Key is the key argument value.
			Key string
		}
	}
	lockAdd    sync.RWMutex
	lockRemove sync.RWMutex
}

// Add calls AddFunc.
func (mock *DedupperMock) Add(key string) bool {
	if mock.AddFunc == nil {
		panic("DedupperMock.AddFunc: method is nil but Dedupper.Add was just called")
	}
	callInfo := struct {
		Key string
	}{
		Key: key,
	}
	mock.lockAdd.Lock()
	mock.calls.Add = append(mock.calls.Add, callInfo)
	mock.lockAdd.Unlock()
	return mock.AddFunc(key)
}

// AddCalls gets all the calls that were made to Add.
// Check the length with:
//     len(mockedDedupper.AddCalls())
func (mock *DedupperMock) AddCalls() []struct {
	Key string
} {
	var calls []struct {
		Key string
	}
	mock.lockAdd.RLock()
	calls = mock.calls.Add
	mock.lockAdd.RUnlock()
	return calls
}

// Remove calls RemoveFunc.
func (mock *DedupperMock) Remove(key string) {
	if mock.RemoveFunc == nil {
		panic("DedupperMock.RemoveFunc: method is nil but Dedupper.Remove was just called")
	}
	callInfo := struct {
		Key string
	}{
		Key: key,
	}
	mock.lockRemove.Lock()
	mock.calls.Remove = append(mock.calls.Remove, callInfo)
	mock.lockRemove.Unlock()
	mock.RemoveFunc(key)
}

// RemoveCalls gets all the calls that were made to Remove.
// Check the length with:
//     len(mockedDedupper.RemoveCalls())
func (mock *DedupperMock) RemoveCalls() []struct {
	Key string
} {
	var calls []struct {
		Key string
	}
	mock.lockRemove.RLock()
	calls = mock.calls.Remove
	mock.lockRemove.RUnlock()
	return calls
}
