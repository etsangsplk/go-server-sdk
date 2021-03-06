package ldclient

import (
	"io/ioutil"
	"log"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/launchdarkly/eventsource"
	"github.com/stretchr/testify/assert"
	"gopkg.in/launchdarkly/go-server-sdk.v4/internal"
)

type testEvent struct {
	id, event, data string
}

func (e *testEvent) Id() string    { return e.id }
func (e *testEvent) Event() string { return e.event }
func (e *testEvent) Data() string  { return e.data }

type testRepo struct {
	initialEvent eventsource.Event
}

func (r *testRepo) Replay(channel, id string) chan eventsource.Event {
	c := make(chan eventsource.Event, 1)
	c <- r.initialEvent
	return c
}

func runStreamingTest(t *testing.T, initialEvent eventsource.Event, test func(events chan<- eventsource.Event, store FeatureStore)) {
	esserver := eventsource.NewServer()
	esserver.ReplayAll = true
	esserver.Register("test", &testRepo{initialEvent: initialEvent})
	events := make(chan eventsource.Event, 1000)
	streamServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/all", r.URL.Path)
		go func() {
			for e := range events {
				esserver.Publish([]string{"test"}, e)
			}
		}()
		esserver.Handler("test").ServeHTTP(w, r)
	}))
	defer streamServer.Close()

	sdkServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/sdk/latest-flags/my-flag", r.URL.Path)
		w.Write([]byte(`{"key": "my-flag", "version": 5}`))
	}))
	defer sdkServer.Close()
	defer esserver.Close()

	store := NewInMemoryFeatureStore(log.New(ioutil.Discard, "", 0))

	cfg := Config{
		FeatureStore: store,
		StreamUri:    streamServer.URL,
		BaseUri:      sdkServer.URL,
		Logger:       log.New(ioutil.Discard, "", 0),
	}

	requestor := newRequestor("sdkKey", cfg, nil)
	sp := newStreamProcessor("sdkKey", cfg, requestor)
	defer sp.Close()

	closeWhenReady := make(chan struct{})

	sp.Start(closeWhenReady)

	select {
	case <-closeWhenReady:
	case <-time.After(time.Second):
		assert.Fail(t, "start timeout")
		return
	}

	test(events, store)
}

func TestStreamProcessor(t *testing.T) {
	t.Parallel()
	initialPutEvent := &testEvent{
		event: putEvent,
		data: `{"path": "/", "data": {
"flags": {"my-flag": {"key": "my-flag", "version": 2}},
"segments": {"my-segment": {"key": "my-segment", "version": 5}}
}}`,
	}

	t.Run("initial put", func(t *testing.T) {
		runStreamingTest(t, initialPutEvent, func(events chan<- eventsource.Event, store FeatureStore) {
			waitForVersion(t, store, Features, "my-flag", 2)
		})
	})

	t.Run("patch flag", func(t *testing.T) {
		runStreamingTest(t, initialPutEvent, func(events chan<- eventsource.Event, store FeatureStore) {
			events <- &testEvent{
				event: patchEvent,
				data:  `{"path": "/flags/my-flag", "data": {"key": "my-flag", "version": 3}}`,
			}

			waitForVersion(t, store, Features, "my-flag", 3)
		})
	})

	t.Run("delete flag", func(t *testing.T) {
		runStreamingTest(t, initialPutEvent, func(events chan<- eventsource.Event, store FeatureStore) {
			events <- &testEvent{
				event: deleteEvent,
				data:  `{"path": "/flags/my-flag", "version": 4}`,
			}

			waitForDelete(t, store, Segments, "my-flag")
		})
	})

	t.Run("patch segment", func(t *testing.T) {
		runStreamingTest(t, initialPutEvent, func(events chan<- eventsource.Event, store FeatureStore) {
			events <- &testEvent{
				event: patchEvent,
				data:  `{"path": "/segments/my-segment", "data": {"key": "my-segment", "version": 7}}`,
			}

			waitForVersion(t, store, Segments, "my-segment", 7)
		})
	})

	t.Run("delete segment", func(t *testing.T) {
		runStreamingTest(t, initialPutEvent, func(events chan<- eventsource.Event, store FeatureStore) {
			events <- &testEvent{
				event: deleteEvent,
				data:  `{"path": "/segments/my-segment", "version": 8}`,
			}

			waitForDelete(t, store, Segments, "my-segment")
		})
	})

	t.Run("indirect flag patch", func(t *testing.T) {
		runStreamingTest(t, initialPutEvent, func(events chan<- eventsource.Event, store FeatureStore) {
			events <- &testEvent{
				event: indirectPatchEvent,
				data:  "/flags/my-flag",
			}

			waitForVersion(t, store, Features, "my-flag", 5)
		})
	})

}

func waitForVersion(t *testing.T, store FeatureStore, kind VersionedDataKind, key string, version int) VersionedData {
	var item VersionedData
	var err error
	deadline := time.Now().Add(time.Second * 3)
	for {
		item, err = store.Get(kind, key)
		if err != nil && item.GetVersion() == version || time.Now().After(deadline) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if assert.NoError(t, err) && assert.NotNil(t, item) && assert.Equal(t, version, item.GetVersion()) {
		return item
	}
	return nil
}

func waitForDelete(t *testing.T, store FeatureStore, kind VersionedDataKind, key string) {
	var item VersionedData
	var err error
	deadline := time.Now().Add(time.Second * 3)
	for {
		item, err = store.Get(kind, key)
		if err != nil && item == nil || time.Now().After(deadline) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	assert.NoError(t, err)
	assert.Nil(t, item)
}

func TestStreamProcessorDoesNotFailImmediatelyOn400(t *testing.T) {
	testStreamProcessorRecoverableError(t, 400)
}

func TestStreamProcessorFailsImmediatelyOn401(t *testing.T) {
	testStreamProcessorUnrecoverableError(t, 401)
}

func TestStreamProcessorFailsImmediatelyOn403(t *testing.T) {
	testStreamProcessorUnrecoverableError(t, 403)
}

func TestStreamProcessorDoesNotFailImmediatelyOn500(t *testing.T) {
	testStreamProcessorRecoverableError(t, 500)
}

func testStreamProcessorUnrecoverableError(t *testing.T, statusCode int) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(statusCode)
	}))
	defer ts.Close()

	id := newDiagnosticId("sdkKey")
	diagnosticsManager := newDiagnosticsManager(id, Config{}, time.Second, time.Now(), nil)
	cfg := Config{
		StreamUri:          ts.URL,
		FeatureStore:       NewInMemoryFeatureStore(log.New(ioutil.Discard, "", 0)),
		Logger:             log.New(ioutil.Discard, "", 0),
		diagnosticsManager: diagnosticsManager,
	}

	sp := newStreamProcessor("sdkKey", cfg, nil)
	defer sp.Close()

	closeWhenReady := make(chan struct{})

	sp.Start(closeWhenReady)

	select {
	case <-closeWhenReady:
		assert.False(t, sp.Initialized())
	case <-time.After(time.Second * 3):
		assert.Fail(t, "Initialization shouldn't block after this error")
	}

	event := diagnosticsManager.CreateStatsEventAndReset(0, 0, 0)
	assert.Equal(t, 1, len(event.StreamInits))
	assert.True(t, event.StreamInits[0].Failed)
}

func testStreamProcessorRecoverableError(t *testing.T, statusCode int) {
	initialPutEvent := &testEvent{
		event: putEvent,
		data: `{"path": "/", "data": {
"flags": {"my-flag": {"key": "my-flag", "version": 2}}, 
"segments": {"my-segment": {"key": "my-segment", "version": 5}}
}}`,
	}
	esserver := eventsource.NewServer()
	esserver.ReplayAll = true
	esserver.Register("test", &testRepo{initialEvent: initialPutEvent})

	attempt := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if attempt == 0 {
			w.WriteHeader(statusCode)
		} else {
			esserver.Handler("test").ServeHTTP(w, r)
		}
		attempt++
	}))
	defer ts.Close()

	id := newDiagnosticId("sdkKey")
	diagnosticsManager := newDiagnosticsManager(id, Config{}, time.Second, time.Now(), nil)
	cfg := Config{
		StreamUri:          ts.URL,
		FeatureStore:       NewInMemoryFeatureStore(log.New(ioutil.Discard, "", 0)),
		Logger:             log.New(ioutil.Discard, "", 0),
		diagnosticsManager: diagnosticsManager,
	}

	sp := newStreamProcessor("sdkKey", cfg, nil)
	defer sp.Close()

	closeWhenReady := make(chan struct{})
	sp.Start(closeWhenReady)

	select {
	case <-closeWhenReady:
		assert.True(t, sp.Initialized())
	case <-time.After(time.Second * 3):
		assert.Fail(t, "Should have successfully retried before now")
	}

	event := diagnosticsManager.CreateStatsEventAndReset(0, 0, 0)
	assert.Equal(t, 2, len(event.StreamInits))
	assert.True(t, event.StreamInits[0].Failed)
	assert.False(t, event.StreamInits[1].Failed)
}

func TestStreamProcessorUsesHTTPClientFactory(t *testing.T) {
	polledURLs := make(chan string, 1)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		polledURLs <- r.URL.Path
		// Don't return a response because we don't want the stream to close and reconnect
	}))
	defer ts.Close()
	defer ts.CloseClientConnections()

	cfg := Config{
		Logger:            log.New(ioutil.Discard, "", 0),
		StreamUri:         ts.URL,
		HTTPClientFactory: urlAppendingHTTPClientFactory("/transformed"),
	}

	sp := newStreamProcessor("sdkKey", cfg, nil)
	defer sp.Close()
	closeWhenReady := make(chan struct{})
	sp.Start(closeWhenReady)

	polledURL := <-polledURLs

	assert.Equal(t, "/all/transformed", polledURL)
}

func TestStreamProcessorDoesNotUseConfiguredTimeoutAsReadTimeout(t *testing.T) {
	polls := make(chan struct{}, 10)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		polls <- struct{}{}
		// Don't return a response because we don't want the stream to close and reconnect
	}))
	defer ts.Close()
	defer ts.CloseClientConnections()

	cfg := Config{
		Logger:    log.New(ioutil.Discard, "", 0),
		StreamUri: ts.URL,
		Timeout:   200 * time.Millisecond,
	}

	sp := newStreamProcessor("sdkKey", cfg, nil)
	defer sp.Close()
	closeWhenReady := make(chan struct{})
	sp.Start(closeWhenReady)

	<-time.After(500 * time.Millisecond)
	assert.Equal(t, 1, len(polls))
}

func TestStreamProcessorRestartsStreamIfStoreNeedsRefresh(t *testing.T) {
	testRepo := &testRepo{
		initialEvent: &testEvent{
			event: putEvent,
			data: `{"path": "/", "data": {
				"flags": {"my-flag": {"key": "my-flag", "version": 1}},
				"segments": {}
				}}`,
		},
	}
	channel := "test"
	esserver := eventsource.NewServer()
	esserver.ReplayAll = true
	esserver.Register(channel, testRepo)

	polls := make(chan struct{}, 10)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		esserver.Handler(channel).ServeHTTP(w, r)
		polls <- struct{}{}
	}))
	defer ts.Close()

	store := &testFeatureStoreWithStatus{
		inits: make(chan map[VersionedDataKind]map[string]VersionedData),
	}
	cfg := Config{
		StreamUri:    ts.URL,
		FeatureStore: store,
		Logger:       log.New(ioutil.Discard, "", 0),
	}

	sp := newStreamProcessor("sdkKey", cfg, nil)
	defer sp.Close()

	closeWhenReady := make(chan struct{})
	sp.Start(closeWhenReady)

	// Wait until the stream has received data and put it in the store
	receivedInitialData := <-store.inits
	assert.Equal(t, 1, receivedInitialData[Features]["my-flag"].GetVersion())

	// Change the stream's initialEvent so we'll get different data the next time it restarts
	testRepo.initialEvent = &testEvent{
		event: putEvent,
		data: `{"path": "/", "data": {
			"flags": {"my-flag": {"key": "my-flag", "version": 2}},
			"segments": {}
			}}`,
	}

	// Make the feature store simulate an outage and recovery with NeedsRefresh: true
	store.publishStatus(internal.FeatureStoreStatus{Available: false})
	store.publishStatus(internal.FeatureStoreStatus{Available: true, NeedsRefresh: true})

	// When the stream restarts, it'll call Init with the refreshed data
	receivedNewData := <-store.inits
	assert.Equal(t, 2, receivedNewData[Features]["my-flag"].GetVersion())
}

type testFeatureStoreWithStatus struct {
	inits     chan map[VersionedDataKind]map[string]VersionedData
	statusSub *testStatusSubscription
}

func (t *testFeatureStoreWithStatus) Get(kind VersionedDataKind, key string) (VersionedData, error) {
	return nil, nil
}

func (t *testFeatureStoreWithStatus) All(kind VersionedDataKind) (map[string]VersionedData, error) {
	return nil, nil
}

func (t *testFeatureStoreWithStatus) Init(data map[VersionedDataKind]map[string]VersionedData) error {
	t.inits <- data
	return nil
}

func (t *testFeatureStoreWithStatus) Delete(kind VersionedDataKind, key string, version int) error {
	return nil
}

func (t *testFeatureStoreWithStatus) Upsert(kind VersionedDataKind, item VersionedData) error {
	return nil
}

func (t *testFeatureStoreWithStatus) Initialized() bool {
	return true
}

func (t *testFeatureStoreWithStatus) GetStoreStatus() internal.FeatureStoreStatus {
	return internal.FeatureStoreStatus{Available: true}
}

func (t *testFeatureStoreWithStatus) StatusSubscribe() internal.FeatureStoreStatusSubscription {
	t.statusSub = &testStatusSubscription{
		ch: make(chan internal.FeatureStoreStatus),
	}
	return t.statusSub
}

func (t *testFeatureStoreWithStatus) publishStatus(status internal.FeatureStoreStatus) {
	if t.statusSub != nil {
		t.statusSub.ch <- status
	}
}

type testStatusSubscription struct {
	ch chan internal.FeatureStoreStatus
}

func (s *testStatusSubscription) Channel() <-chan internal.FeatureStoreStatus {
	return s.ch
}

func (s *testStatusSubscription) Close() {
	close(s.ch)
}
