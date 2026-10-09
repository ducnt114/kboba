# kboba: nhật ký học tập

Tài liệu này ghi lại kboba được xây dựng như thế nào: mỗi phase làm gì, học được gì, và mỗi ý tưởng nằm ở đâu trong code. Mục đích là để sau này đọc lại code thì hiểu được *tại sao* nó được viết như vậy.

> Thuật ngữ kỹ thuật được giữ nguyên tiếng Anh. Code được tham chiếu theo **file + tên symbol** thay vì số dòng, vì số dòng sẽ thay đổi khi code thay đổi. Dùng `grep -n "func (v podsView) Update" -r internal/` để nhảy tới đúng chỗ.

## Mục lục

1. [Bức tranh tổng thể](#1-bức-tranh-tổng-thể)
2. [Phase (a): Skeleton, kubeconfig, contexts view](#2-phase-a-skeleton-kubeconfig-contexts-view)
3. [Phase (b): Namespaces view](#3-phase-b-namespaces-view)
4. [Phase (c): Pods table với informer](#4-phase-c-pods-table-với-informer)
5. [Phase (d): Logs view](#5-phase-d-logs-view)
6. [Phase (e): Describe view, filter, help](#6-phase-e-describe-view-filter-help)
7. [Các pattern xuyên suốt](#7-các-pattern-xuyên-suốt)
8. [Chiến lược test](#8-chiến-lược-test)
9. [Những cái bẫy đã gặp](#9-những-cái-bẫy-đã-gặp)
10. [Các phase tiếp theo](#10-các-phase-tiếp-theo)
11. [Thứ tự đọc code đề xuất](#11-thứ-tự-đọc-code-đề-xuất)

Mỗi phase là một commit riêng. Xem toàn bộ thay đổi của một phase bằng `git show <hash>`:

| Phase | Commit    | Nội dung                                  |
|-------|-----------|-------------------------------------------|
| (a)   | `748f140` | Skeleton, kubeconfig, contexts view       |
| (b)   | `ab80fe8` | Namespaces view                           |
| (c)   | `bcbd957` | Pods table với informer, kind Makefile    |
| (d)   | `5730086` | Logs view                                 |
| (e)   | `f1e5eaf` | Describe view, help, README               |

---

## 1. Bức tranh tổng thể

### Ranh giới package

```
cmd/kboba/main.go     flags → tạo ClientFactory → chạy tea.Program
        │
        ▼
internal/ui           Bubble Tea. Biết về k8s.Client (interface), KHÔNG biết client-go
        │  gọi qua interface k8s.Client (7 method, tất cả đều là read)
        ▼
internal/k8s          client-go. KHÔNG import bubbletea
        │
        ▼
   Kubernetes API     chỉ nhận GET (list, watch, get, pods/log)
```

Có hai quy tắc giúp code dễ hiểu và dễ test:

- **`internal/k8s` không biết UI tồn tại.** Package này trả về dữ liệu (`PodInfo`, `ContextInfo`, `string`) và channel. Nó không trả về `tea.Msg`. Vì vậy test của nó chỉ cần fake clientset của client-go.
- **`internal/ui` chỉ thấy interface `k8s.Client`** (`internal/k8s/client.go`). Test UI thay interface này bằng `fakeClient` trong `internal/ui/root_test.go`, không cần cluster.

### Vòng lặp Elm của Bubble Tea

```
           ┌──────────── tea.Msg ◀───────── tea.Cmd chạy trong goroutine riêng
           ▼                                   (gọi API, chờ channel, timer…)
   Update(msg) → (model mới, tea.Cmd) ──────────┘
           │
           ▼
        View() → string → terminal
```

- `Update` phải **nhanh và không block**. Mọi việc chậm được gói vào một `tea.Cmd`, tức là một hàm `func() tea.Msg`. Bubble Tea chạy hàm đó trong goroutine riêng rồi đưa kết quả trở lại `Update` dưới dạng message.
- `View` là hàm thuần: chỉ đọc model rồi trả về string.

### Root model và sub-models

`internal/ui/root.go` → `type Model` là root model. Nó chỉ **điều phối**:

- giữ state chung: `client`, `context`, `namespace`, view đang active;
- vẽ phần khung: header, command bar, status bar, help bar;
- định tuyến message tới đúng sub-model.

Mỗi màn hình là một sub-model riêng, và tất cả có cùng "hợp đồng":

| Method              | Ý nghĩa                                                      |
|---------------------|--------------------------------------------------------------|
| `Update(msg)`       | trả về `(view mới, tea.Cmd)`, đúng kiểu Elm (value receiver) |
| `View()`            | render body của view                                         |
| `SetSize(w, h)`     | nhận kích thước body từ root khi cửa sổ thay đổi             |
| `capturingInput()`  | `true` khi view đang có ô nhập text (ví dụ filter)           |
| `keys()`            | keybinding của view, dùng cho help bar                       |

| Sub-model        | File                       | Component bubbles |
|------------------|----------------------------|-------------------|
| `contextsView`   | `internal/ui/contexts.go`   | `list`            |
| `namespacesView` | `internal/ui/namespaces.go` | `list`            |
| `podsView`       | `internal/ui/pods.go`       | `table`, `textinput` |
| `logsView`       | `internal/ui/logs.go`       | `viewport`        |
| `describeView`   | `internal/ui/describe.go`   | `viewport`        |

Mình **không** gom các sub-model vào một interface chung. Root dùng `switch m.active` trong 5 hàm: `updateActive`, `activeCapturingInput`, `activeKeys`, `layout` và `View`. Cách này hơi lặp lại, nhưng tường minh: đọc vào là biết ngay message đi đâu.

---

## 2. Phase (a): Skeleton, kubeconfig, contexts view

### Mục tiêu
Có một chương trình chạy được: đọc kubeconfig, liệt kê context, Enter để chuyển context (chỉ trong bộ nhớ).

### Học được gì
- Cách `clientcmd` load kubeconfig: thứ tự `$KUBECONFIG` → `~/.kube/config`, và override context mà không ghi file.
- Cấu trúc tối thiểu của một chương trình Bubble Tea: `Init`, `Update`, `View`, `tea.NewProgram`.
- Cách dùng component `list` của bubbles và cấu hình lại keymap mặc định của nó.
- Đưa lỗi lên status bar thay vì panic.

### Thể hiện trong code

**Load kubeconfig không ghi file:** `internal/k8s/client.go` → `NewClient`

```go
rules := clientcmd.NewDefaultClientConfigLoadingRules()
if kubeconfigPath != "" {
    rules.ExplicitPath = kubeconfigPath
}
overrides := &clientcmd.ConfigOverrides{CurrentContext: contextName}
cc := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(rules, overrides)
```

- `ConfigOverrides{CurrentContext: ...}` là cách "chọn context" chỉ trong bộ nhớ. Không có dòng nào gọi `clientcmd.ModifyConfig` hay `WriteToFile`.
- `cc.RawConfig()` trả về toàn bộ kubeconfig (dùng để liệt kê context). `cc.ClientConfig()` trả về `*rest.Config` cho context đã chọn.

**Context lỗi không làm chết app:** struct `client` có field `connErr`. Nếu context không tồn tại, `NewClient` vẫn trả về client, kèm `connErr != nil`. Các method gọi cluster trả về `connErr`, còn `ListContexts` vẫn hoạt động, nhờ đó người dùng vẫn chọn được context khác. `NewClient` chỉ trả lỗi khi chính file kubeconfig không đọc được (`main.go` kiểm tra điều này trước khi khởi động TUI).

**Transport chỉ cho GET:** `readOnlyTransport` trong `client.go`, được gắn vào bằng `cfg.Wrap(newReadOnlyTransport)`. Mọi request có method khác `GET`/`HEAD` đều bị từ chối trước khi rời khỏi máy.

**Kết nối không block UI:** `internal/ui/root.go` → `connect()` trả về một `tea.Cmd`. Việc tạo client và đọc kubeconfig chạy trong goroutine, rồi kết quả quay về dưới dạng `clientReadyMsg`. `handleClientReady` xử lý cả hai trường hợp:
- thành công: cập nhật `m.client`, `m.context`, `m.namespace`;
- thất bại: giữ nguyên context cũ, hiện lỗi, mở contexts view cho người dùng chọn lại.

**Thứ tự ưu tiên namespace** (`handleClientReady`): flag `--namespace`, chỉ áp dụng ở lần kết nối đầu → namespace của context → `"default"`.

**Dependency injection cho test:** `ui.New` nhận một `ClientFactory func(contextName string) (k8s.Client, error)`. `main.go` truyền vào hàm gọi `k8s.NewClient`, còn test truyền `fakeFactory`.

**Cấu hình `list`:** `internal/ui/listview.go` → `newList`. Mặc định `list` coi `q`/`esc` là Quit và **tự trả về `tea.Quit`**. Mình phải tắt `KeyMap.Quit` và `KeyMap.ForceQuit`, vì quyền thoát app thuộc về root.

**Tắt klog:** `cmd/kboba/main.go` có dòng `klog.SetLogger(logr.Discard())`. client-go ghi log ra stderr qua klog; nếu không tắt, log sẽ in đè lên giao diện TUI.

### Test liên quan
- `internal/k8s/contexts_test.go`: dùng kubeconfig tạm. Kiểm tra đánh dấu `Current`, context không tồn tại không gây lỗi chết, và **file kubeconfig không bị sửa** (`TestNewClientDoesNotModifyKubeconfig`).
- `internal/k8s/readonly_test.go` → `TestReadOnlyTransportRejectsWrites`.
- `internal/ui/root_test.go` → `TestSwitchContext`, `TestSwitchToUnknownContextKeepsCurrent`.

---

## 3. Phase (b): Namespaces view

### Mục tiêu
Liệt kê namespace, có thêm mục "(all namespaces)", Enter để chọn. Thêm lệnh `:ns [name|all]`.

### Học được gì
- Lời gọi API đầu tiên: `CoreV1().Namespaces().List(ctx, ...)`.
- Đặt timeout cho các call một lần bằng `context.WithTimeout`.
- Thiết kế UX khi bị RBAC chặn: degrade thay vì chết.
- Dùng fake clientset của client-go để test.

### Thể hiện trong code

**Gọi API:** `internal/k8s/namespaces.go` → `ListNamespaces`. Hàm kiểm tra `connErr`, gọi `List`, chuyển kết quả sang `[]string` và sort. UI không bao giờ thấy `corev1.Namespace`.

**Timeout:** `internal/ui/namespaces.go` → `loadNamespaces` bọc call trong `context.WithTimeout(..., requestTimeout)`. Hằng `requestTimeout` nằm ở `internal/ui/messages.go`. Nếu cluster không trả lời, Cmd vẫn kết thúc và lỗi hiện lên status bar.

**"All namespaces" là chuỗi rỗng:** hằng `allNamespaces = ""` trong `namespaces.go`, khớp với `metav1.NamespaceAll`. Nhờ vậy namespace của informer cũng nhận `""` cho "tất cả" mà không cần chuyển đổi.

**Degrade khi bị forbidden:** trong `namespacesView.Update` → `case namespacesLoadedMsg`, nếu có lỗi thì list vẫn có "(all namespaces)" và namespace hiện tại. Người dùng vẫn gõ được `:ns <tên>`.

**Message điều hướng đi từ view lên root:** view không tự đổi namespace. Nó phát ra `namespaceSelectedMsg`, root bắt message này và gọi `switchNamespace`. Đây là pattern "con báo, cha quyết": sub-model không đụng vào state của root.

**Hàm dùng chung cho các list:** `updateList` trong `listview.go` xử lý Enter (chọn) và Esc (back nếu không có filter) cho cả contexts lẫn namespaces.

### Test liên quan
- `internal/k8s/fake_test.go` → `newFakeClient`: tạo `client` dựa trên `fake.NewSimpleClientset(objs...)`.
- `internal/k8s/readonly_test.go` → `TestClientOnlyReads`: gọi mọi method rồi duyệt `cs.Actions()`, assert verb chỉ là `get/list/watch`.
- `internal/ui/root_test.go` → `TestNamespaceCommands`, `TestNamespacesForbiddenFallsBack`.

---

## 4. Phase (c): Pods table với informer

Đây là phase quan trọng nhất về mặt kiến trúc.

### Mục tiêu
Bảng pods tự cập nhật realtime bằng informer, không poll. Đổi context/namespace thì stop informer cũ trước khi tạo cái mới.

### Học được gì
- `SharedInformerFactory`, event handler, `WaitForCacheSync`, `Shutdown`.
- Biến một channel thành dòng message của Bubble Tea (pattern "subscription").
- Tránh race và leak khi cần dừng một nguồn dữ liệu bất đồng bộ.
- Cách `kubectl` tính các cột STATUS, READY, RESTARTS.
- Component `table`: cột co giãn theo chiều rộng, giữ cursor khi dữ liệu thay đổi.

### Thể hiện trong code

#### 4.1 Informer → channel (`internal/k8s/pods.go` → `WatchPods`)

```
factory := informers.NewSharedInformerFactoryWithOptions(cs, 0, informers.WithNamespace(ns))
informer := factory.Core().V1().Pods().Informer()
informer.SetWatchErrorHandler(...)   → PodEvent{Type: PodWatchFailed}
informer.AddEventHandler(Add/Update/Delete) → PodEvent{PodUpserted | PodDeleted}
factory.Start(stopCh)
goroutine: WaitForCacheSync → PodEvent{Type: PodsSynced}
```

Những điểm cần chú ý:

- **Handler không bao giờ block mãi.** Hàm `send` dùng `select { case events <- ev: case <-stopCh: }`. Nếu UI đã dừng đọc thì handler thoát ngay khi stop được gọi.
- **`SetWatchErrorHandler`** phải gọi *trước* `Start`. Không có nó, lỗi forbidden hay mất kết nối chỉ được client-go ghi vào klog (đã bị tắt), và người dùng sẽ không thấy gì.
- **Tombstone:** trong `DeleteFunc`, object có thể là `cache.DeletedFinalStateUnknown` khi watch bị lỡ sự kiện delete. Phải lấy `.Obj` ra trước khi ép kiểu.
- **Thứ tự dừng** (closure `stop`):

  ```go
  close(stopCh)            // 1. báo informer + handler dừng
  go func() {
      factory.Shutdown()   // 2. chờ mọi goroutine của informer (kể cả handler) thoát
      wg.Wait()            // 3. chờ goroutine WaitForCacheSync
      close(events)        // 4. lúc này mới an toàn để đóng channel
  }()
  ```

  Nếu đóng `events` khi handler còn có thể gửi vào, chương trình sẽ panic (`send on closed channel`). Nếu không bao giờ đóng, Cmd đang chờ trên channel bị kẹt mãi, tức là leak goroutine. Bước 2–4 chạy trong goroutine để `Stop()` không block `Update`. `sync.Once` giúp `Stop` gọi nhiều lần vẫn an toàn.

- **`PodWatch` là struct có field hàm** (`Events`, `Stop func()`) thay vì có method. Nhờ vậy test UI tự tạo được một `PodWatch` giả mà không cần constructor.

#### 4.2 Channel → message của Bubble Tea (`internal/ui/pods.go`)

Pattern "chờ một message rồi đăng ký lại" (subscription):

```go
func waitForPodEvents(w *k8s.PodWatch, gen int) tea.Cmd {
    return func() tea.Msg {
        events, ok := receiveBatch(w.Events, 500)   // block ở goroutine của Cmd, không phải Update
        if !ok {
            return podWatchClosedMsg{gen: gen}
        }
        return podEventsMsg{events: events, gen: gen}
    }
}

// trong Update:
case podEventsMsg:
    ... áp dụng events vào v.pods ...
    return v, tea.Batch(status, waitForPodEvents(v.watch, v.gen)) // đăng ký lại
```

Mỗi lần nhận một batch, `Update` trả về một Cmd mới để chờ batch tiếp theo. Vì vậy luôn có **đúng một** Cmd đang chờ trên channel.

**`receiveBatch`** (`internal/ui/stream.go`) chờ 1 phần tử, rồi lấy thêm những phần tử đang có sẵn mà không chờ, tối đa `limit`. Khi informer gửi danh sách ban đầu (có thể hàng trăm pod), app chỉ chạy 1 lần `Update` + render thay vì hàng trăm lần. Hàm generic `[T any]` này được dùng lại cho log stream ở phase (d).

#### 4.3 Generation counter: chống message cũ

`podsView` có field `gen int`. Mọi message của watch mang theo `gen`:

```
start():  stop()  →  gen++  →  Cmd(WatchPods) → podWatchStartedMsg{gen}
Update:   msg.gen != v.gen  →  bỏ qua message
```

Tình huống nó giải quyết: bạn đổi namespace từ `a` sang `b`. Nhưng một `podEventsMsg` của `a` đã nằm sẵn trong hàng đợi message của Bubble Tea, nên `stop()` không thu hồi được nó. Nếu không có `gen`, pod của `a` sẽ lọt vào bảng của `b`.

Một trường hợp đặc biệt cần xử lý thêm: nếu `podWatchStartedMsg` đến với `gen` cũ, tức là người dùng đã đổi namespace *trước khi* watch kịp khởi động, thì phải gọi `msg.watch.Stop()`. Nếu không, informer đó chạy mãi mà không ai dừng.

#### 4.4 Đổi context/namespace

Trong `internal/ui/root.go`:
- `switchNamespace` gọi `m.pods.start(m.client, ns)`.
- `handleClientReady` (sau khi đổi context) cũng gọi `m.pods.start(...)`.

Bên trong, `start()` gọi `stop()` trước tiên, nên informer cũ luôn được dừng *trước khi* informer mới được tạo.

Các message của watch (`podWatchStartedMsg`, `podEventsMsg`, `podWatchClosedMsg`, `ageTickMsg`) luôn được root chuyển cho `m.pods`, **dù view nào đang active**. Nhờ vậy khi bạn đang xem logs, bảng pods vẫn được cập nhật ở phía sau.

#### 4.5 Format giống kubectl (`internal/k8s/pods.go`)

- `NewPodInfo` chuyển `*corev1.Pod` thành `PodInfo`: READY (`ready/total`), RESTARTS (tổng của các container), danh sách container và `DefaultContainer` (đọc từ annotation `kubectl.kubernetes.io/default-container`).
- `podStatus` là bản rút gọn logic của kubectl, theo thứ tự ưu tiên:
  1. `Terminating` (nếu có `DeletionTimestamp`);
  2. init container chưa xong (`Init:Error`, `Init:1/2`…);
  3. lý do waiting/terminated của container (`CrashLoopBackOff`, `OOMKilled`, `ExitCode:2`…);
  4. phase hoặc `Status.Reason`.

Đây là logic thuần nên được test kỹ trong `pods_test.go` → `TestPodStatus`.

#### 4.6 Table (`internal/ui/pods.go`)

- `setColumns`: cột NAME lấy phần chiều rộng còn lại, cột NAMESPACE chỉ hiện khi đang xem all namespaces. Trước khi `SetColumns` phải `SetRows(nil)`, vì table sẽ index `row[i]` theo số cột. Đổi từ 5 lên 6 cột mà rows cũ chỉ có 5 cell sẽ gây panic.
- `setRows(selectedKey)`: rebuild các row từ map, áp dụng filter, sort theo `namespace/name`, và **giữ cursor trên đúng pod** bằng key thay vì index. `rowKeys` song song với rows để tra ngược từ cursor ra `PodInfo`.
- `ageTick`: timer UI 5 giây để cột AGE tự cập nhật. Timer này không gọi cluster. Chỉ có một chuỗi tick, bắt đầu từ `Init`.

#### 4.7 Hạ tầng kind (`Makefile`, `hack/kind/`)

- Cluster kind có kubeconfig riêng (`.kind/admin.kubeconfig`), nên `~/.kube/config` không bị đụng tới. Mọi lệnh `kubectl` trong Makefile đều chỉ định cả `--kubeconfig` và `--context`.
- `rbac.yaml` có hai ServiceAccount:
  - `kboba-readonly`: ClusterRole với get/list/watch;
  - `kboba-limited`: Role chỉ trong `kboba-demo`, dùng để thử lỗi forbidden.
- `readonly-kubeconfig.sh` sinh kubeconfig dùng token của các SA trên, thêm một context trỏ tới endpoint chết để thử lỗi kết nối.

### Test liên quan
- `internal/k8s/pods_test.go`: `TestPodStatus` (bảng case), `TestNewPodInfo`, `TestWatchPods` (tạo/xóa pod trên fake clientset, kiểm tra event, kiểm tra `Stop` đóng channel).
- `internal/ui/root_test.go`: `TestPodsWatchLifecycle` (đổi ns thì watch cũ bị stop, event có `gen` cũ bị bỏ qua, có cột NAMESPACE), `TestPodsWatchStoppedOnContextSwitch`, `TestPodsFilter`.
- `internal/ui/format_test.go`: `TestReceiveBatch`.

---

## 5. Phase (d): Logs view

### Mục tiêu
Enter trên pod để xem log bằng `viewport`. `f` bật/tắt follow, `c` đổi container, Esc quay lại. Không leak goroutine hay connection.

### Học được gì
- `GetLogs(...).Stream(ctx)` trả về một `io.ReadCloser` sống lâu (HTTP chunked response).
- `bufio.Scanner` và giới hạn kích thước dòng.
- Dùng `context.WithCancel` làm công tắc tắt cho cả HTTP connection lẫn goroutine.
- Ring buffer, và chi phí render của `viewport`.
- Xử lý xung đột keymap giữa component có sẵn và app.

### Thể hiện trong code

**Stream log** (`internal/k8s/logs.go` → `StreamLogs`):

```go
body, err := c.clientset.CoreV1().Pods(ns).GetLogs(pod, opts).Stream(ctx) // opts: Follow, TailLines=500, Container
go func() {
    defer close(lines)    // chạy SAU CÙNG (defer chạy theo thứ tự ngược)
    defer body.Close()
    sc := bufio.NewScanner(body)
    sc.Buffer(make([]byte, 64*1024), maxLogLineBytes) // mặc định chỉ 64KB/dòng
    for sc.Scan() {
        select {
        case lines <- sc.Text():
        case <-ctx.Done():
            return
        }
    }
    if err := sc.Err(); err != nil && ctx.Err() == nil {
        errs <- err       // gửi lỗi TRƯỚC khi close(lines)
    }
}()
```

- `Stream(ctx)` gắn body của response với `ctx`. Khi `cancel()` được gọi, lệnh read đang block sẽ trả về lỗi, `Scan()` kết thúc và goroutine thoát. Connection cũng được đóng.
- `errs` có buffer 1 và được ghi *trước* `close(lines)`. Phía UI, khi thấy `lines` đã đóng thì đọc `errs` không chờ (select với `default`), và chắc chắn thấy lỗi nếu có.
- Lỗi do chính mình cancel (`ctx.Err() != nil`) không được coi là lỗi.

**Phía UI** (`internal/ui/logs.go`):

- `open` gọi `restart()`. `restart` làm các bước: `stop()` (cancel stream cũ) → `gen++` → reset buffer → tạo `ctx, cancel` mới → trả về Cmd gọi `StreamLogs`.
- `waitForLogLines` dùng lại đúng pattern subscription và `receiveBatch` của phase (c).
- View lưu `lines`/`errs` của stream hiện tại vào field, để `Update` đăng ký lại (giống `podsView.watch`).
- **Cancel khi rời view:** đặt ở *một chỗ duy nhất*, `Model.setActive` trong `root.go`:

  ```go
  if m.active == viewLogs && v != viewLogs {
      m.logs.stop()
  }
  ```

  Mọi đường rời khỏi logs view đều đi qua `setActive`: Esc, `:pods`, `:ns x`, đổi context. Vì vậy không có đường nào quên cancel.

**Ring buffer** (`internal/ui/logbuffer.go`): mảng cố định `maxLogLines = 5000` phần tử cùng hai chỉ số `start`, `size`. `push` có độ phức tạp O(1) và không cấp phát lại bộ nhớ; khi đầy thì ghi đè dòng cũ nhất. `String()` tính trước tổng độ dài rồi `Grow` một lần.

**Render một lần mỗi batch:** `viewport` của bubbles v1 chỉ nhận `SetContent(string)`, nên không tránh hoàn toàn được việc join chuỗi. Bù lại, mình chỉ join **một lần cho mỗi batch** (`case logLinesMsg`). Một log xả 1000 dòng/giây sẽ chỉ tốn vài lần render, không phải 1000 lần.

**Follow** (`handleKey`): stream luôn chạy; `follow` chỉ quyết định có `GotoBottom()` sau mỗi batch hay không. Cuộn lên (`!viewport.AtBottom()`) sẽ tự tắt follow; bấm `f` để bật lại.

**Xung đột phím:** keymap mặc định của viewport gán `f` cho PageDown. Trong `newLogsView` mình định nghĩa lại `vp.KeyMap.PageDown` chỉ với `pgdown` và space.

**Đổi container:** `nextContainer` lấy container kế tiếp theo vòng tròn, rồi `restart()`. Stream cũ bị cancel, `gen` tăng, nên dòng log cũ còn đang trên đường tới sẽ bị bỏ qua.

### Test liên quan
- `internal/k8s/logs_test.go`: fake clientset trả về body `"fake logs"`. Kiểm tra action là `get` trên subresource `log`, và cancel thì channel đóng.
- `internal/ui/logbuffer_test.go`: ghi đè vòng tròn, đúng 5000 dòng cuối.
- `internal/ui/root_test.go`: `TestLogsLifecycle` (follow, đổi container thì stream cũ bị cancel, Esc cũng cancel), `TestLogsCancelledOnNamespaceSwitch`. `fakeClient.StreamLogs` lưu lại `ctx` để test kiểm tra `ctx.Err() != nil`.

---

## 6. Phase (e): Describe view, filter, help

### Mục tiêu
Phím `d` hiện chi tiết pod (status, containers, conditions, events). Filter `/` và help `?` hoạt động nhất quán. Có README.

### Học được gì
- Field selector khi list events (`involvedObject.name=...`).
- `text/tabwriter` để căn cột.
- Tách logic format thành hàm thuần (nhận `now time.Time`) để test được.
- Wrap text theo chiều rộng terminal bằng lipgloss.
- Định tuyến phím khi có ô nhập text đang focus.

### Thể hiện trong code

**Describe** (`internal/k8s/describe.go`):
- `DescribePod` = `GetPod` + `Events(ns).List(FieldSelector: involvedObject.kind/name/namespace)`.
- Lỗi khi list events (ví dụ thiếu quyền RBAC) **không làm hỏng** cả describe: lỗi được in vào mục `Events:` trong output.
- Có lọc lại phía client (`ev.InvolvedObject.Name == name`), vì không phải server nào (hay fake clientset) cũng tôn trọng field selector.
- `formatPodDescription(pod, events, eventsErr, now)` là hàm thuần, nhận `now` làm tham số nên test không phụ thuộc thời gian thực. Bên trong dùng `duration.HumanDuration` của apimachinery để hiển thị thời gian giống kubectl.

**Describe view** (`internal/ui/describe.go`):
- `describeLoadedMsg` mang `key` của pod; kết quả không khớp với pod đang xem sẽ bị bỏ qua. Đây là biến thể của generation counter: dùng key thay cho số đếm.
- `render()` wrap text bằng `lipgloss.NewStyle().Width(w)`, và được gọi lại trong `SetSize` để wrap lại khi cửa sổ đổi kích thước.
- Phím `r` để refresh, vì describe là một snapshot chứ không phải dữ liệu live.

**Định tuyến phím** (`internal/ui/root.go` → `handleKey`), theo thứ tự ưu tiên:

```
1. ctrl+c                  → thoát, luôn luôn
2. đang ở command mode     → mọi phím vào textinput của command bar
3. view đang nhập text     → (capturingInput) mọi phím vào view, kể cả "q" và ":"
4. phím global             → q, :, ?
5. còn lại                 → view đang active
```

Bước 3 là lý do gõ `q` vào ô filter không làm thoát app (`TestQuitKeyIgnoredWhileTyping`).

**Filter:**
- Pods view có `textinput` riêng; `/` để focus, Enter giữ filter, Esc xóa filter.
- Các view dạng list dùng filter có sẵn của bubbles `list`.

**Help** (`internal/ui/keys.go`):
- `helpKeys` implement `help.KeyMap`: `ShortHelp` = phím của view + phím global, `FullHelp` = 3 cột.
- `?` bật/tắt `help.ShowAll`, sau đó gọi `layout()` vì chiều cao help bar thay đổi.
- Pods view chỉ hiện `esc clear filter` khi đang có filter (`podsView.keys`).

**Layout** (`Model.layout` và `Model.View`): chiều cao body = chiều cao cửa sổ − header − status − help. Body được ép đúng chiều cao (`Height` + `MaxHeight`) để status bar và help bar luôn nằm sát đáy, không bị nhảy lên khi danh sách ngắn.

### Test liên quan
- `internal/k8s/describe_test.go`: nội dung output, events của pod khác bị loại, pod không tồn tại thì trả lỗi, lỗi list events được in ra.
- `internal/ui/root_test.go`: `TestDescribe` (kết quả cũ bị bỏ qua), `TestDescribeDeletedPodShowsError`, `TestQuitKeyIgnoredWhileTyping`.

---

## 7. Các pattern xuyên suốt

| Pattern | Ý tưởng | Ở đâu |
|---|---|---|
| **Không block trong `Update`** | Mọi I/O đều nằm trong một `tea.Cmd` | `connect`, `loadContexts`, `loadNamespaces`, `podsView.start`, `logsView.restart`, `describeView.load` |
| **Subscription channel → Msg** | Cmd chờ channel, `Update` xử lý xong thì trả về Cmd chờ tiếp | `waitForPodEvents`, `waitForLogLines` |
| **Batching** | Chờ 1 phần tử, lấy thêm những gì có sẵn | `receiveBatch` (`stream.go`) |
| **Generation / key guard** | Message mang theo "phiên"; phiên cũ thì bỏ qua | `podsView.gen`, `logsView.gen`, `describeLoadedMsg.key` |
| **Stop rồi mới close** | Đóng stop channel → chờ producer thoát → đóng data channel | `WatchPods` (closure `stop`), `StreamLogs` (defer) |
| **Một chỗ cleanup** | Rời logs view luôn đi qua `setActive` | `Model.setActive` |
| **Con báo, cha quyết** | View phát message ý định; root đổi state | `contextSelectedMsg`, `namespaceSelectedMsg`, `openLogsMsg`, `openDescribeMsg`, `backMsg` |
| **Lỗi lên status bar** | View trả về `reportErr(err)`, root hiển thị | `messages.go` → `statusMsg`, `reportErr`, `reportInfo` |
| **Domain type thay vì API type** | k8s trả `PodInfo` đã format sẵn; UI không cần hiểu `corev1` | `NewPodInfo` |
| **Read-only 3 lớp** | Allowlist interface + kiểm tra action của fake + transport chỉ GET | `readonly_test.go`, `readOnlyTransport` |

### Vì sao sub-model dùng value receiver cho `Update`?

`func (v podsView) Update(msg) (podsView, tea.Cmd)` trả về một bản copy mới, đúng tinh thần Elm (state bất biến). Root gán lại: `m.pods, cmd = m.pods.Update(msg)`. Các helper thay đổi state như `start`, `stop`, `SetSize` dùng pointer receiver và chỉ được gọi trên bản copy cục bộ của root (`m` trong `Update` là biến cục bộ, có thể lấy địa chỉ). Lưu ý: map và slice bên trong vẫn chia sẻ vùng nhớ giữa các bản copy. Điều này ổn vì bản copy cũ luôn bị bỏ đi ngay sau mỗi `Update`.

---

## 8. Chiến lược test

### `internal/k8s`: fake clientset
- `newFakeClient(t, objs...)` (`fake_test.go`) tạo `client` từ `fake.NewSimpleClientset`.
- Fake ghi lại mọi request vào `cs.Actions()`, và đây là nền tảng của `TestClientOnlyReads`.
- Fake **hỗ trợ watch**, nên `TestWatchPods` chạy informer thật trên fake. Test tạo/xóa pod qua fake để giả lập hoạt động của cluster; đây là thao tác ghi lên *fake*, không phải do kboba thực hiện.
- Fake `GetLogs` trả về body cố định `"fake logs"`.

### `internal/ui`: fake `k8s.Client`
- `fakeClient` trong `root_test.go` implement đủ 7 method:
  - `WatchPods` trả về channel đã nạp sẵn event, và ghi nhận `stopped`;
  - `StreamLogs` lưu lại `ctx` để kiểm tra việc cancel.
- Helper `send(t, m, msg)` gọi `Update`, chạy các Cmd trả về, đưa message kết quả trở lại `Update` (đệ quy tối đa 5 cấp).
- `runCmd` chạy Cmd trong goroutine với **timeout 50ms**. Những Cmd không xong ngay sẽ bị bỏ qua: con trỏ nhấp nháy của textinput (có sleep), tick 5 giây, hay Cmd đang chờ channel. Đây là lý do test UI mất vài giây.
- Vì Cmd chờ bị bỏ lại vẫn có thể "ăn" event gửi vào channel sau đó, nên fake **nạp event trước** khi trả watch về, thay vì đẩy event vào giữa chừng.
- `typeCommand(t, m, "ns all")` giả lập gõ `:ns all` rồi Enter.

### Kiểm thử thủ công
- `make run-kind`: cluster thật với quyền read-only thật.
- Trong lúc phát triển, mình còn chạy `kube-apiserver` + `etcd` của envtest (không có kubelet) và điều khiển TUI qua `tmux send-keys` / `tmux capture-pane` để kiểm tra realtime update và lỗi RBAC.

---

## 9. Những cái bẫy đã gặp

| Bẫy | Triệu chứng | Cách xử lý |
|---|---|---|
| `list` tự xử lý Quit | `q`/`esc` trong list trả về `tea.Quit` | Tắt `KeyMap.Quit`, `KeyMap.ForceQuit` trong `newList` |
| viewport gán `f` cho PageDown | `f` không bật/tắt follow mà lật trang | Định nghĩa lại `vp.KeyMap.PageDown` |
| table gán `d` cho half page down | (có thể nuốt phím describe) | `podsView.handleKey` kiểm tra phím describe trước khi chuyển cho table |
| klog ghi ra stderr | Log của client-go in đè lên TUI | `klog.SetLogger(logr.Discard())` |
| Đóng channel khi handler còn gửi | panic `send on closed channel` | `close(stopCh)` → `Shutdown()` → `close(events)` |
| Không đóng channel events | Cmd chờ bị kẹt mãi, leak goroutine | `Stop` luôn đóng `events` sau khi shutdown |
| Message stale sau khi đổi ns | Pod của namespace cũ lọt vào bảng | Generation counter |
| Watch khởi động sau khi đã đổi ns | Informer mồ côi chạy mãi | Gặp `podWatchStartedMsg` có `gen` cũ thì gọi `Stop()` |
| `SetColumns` khi rows có ít cell hơn số cột | panic index out of range | `SetRows(nil)` trước `SetColumns` |
| `bufio.Scanner` mặc định 64KB/dòng | Dòng log dài làm stream dừng | `sc.Buffer(..., 1MB)` |
| Context không tồn tại | Mất client hiện tại | `connect` trả lỗi kèm danh sách context; giữ client cũ |
| `Init` trả về `tea.Batch` | Test gửi nhầm `BatchMsg` vào `Update` | `runCmd` mở rộng `BatchMsg` |
| Fake clientset bỏ qua field selector | Events của pod khác lọt vào describe | Lọc lại phía client |
| Pod chưa được schedule | Logs rỗng, không có lỗi | Đúng hành vi của API server (`kubectl logs` cũng vậy) |

---

## 10. Các phase tiếp theo

Thứ tự đề xuất: **1 → 3 → 4 → 2 → 6 → 8 → 9**. Mọi tính năng vẫn giữ nguyên tắc read-only.

| # | Tính năng | Học được gì | Gợi ý triển khai |
|---|---|---|---|
| 1 | View cho Deployments, Services, Events, Nodes | Tạo abstraction sau khi đã có ví dụ thật; một informer phục vụ nhiều consumer | Tách `podsView` thành `resourceView` nhận vào: danh sách cột, hàm object → row, hàm tạo informer |
| 2 | Drill-down Deployment → Pods | Navigation stack trong Elm | Thay `backMsg` → `viewPods` bằng một stack; dùng `informers.WithTweakListOptions` để lọc theo label selector |
| 3 | Xem YAML (`y`) | Serialize object, highlight cú pháp | `sigs.k8s.io/yaml` + `alecthomas/chroma`; lọc bỏ `managedFields` |
| 4 | Tìm kiếm trong log, wrap, timestamps, `--previous` | Thao tác trên ring buffer, highlight | `PodLogOptions.Timestamps`, `Previous: true` (rất hữu ích với pod crashloop) |
| 5 | Sắp xếp cột | Logic thuần, dễ test | Sort trong `setRows` theo một `sortKey` |
| 6 | Tô màu theo status | Giới hạn của component có sẵn | bubbles `table` cắt chuỗi theo độ dài nên lệch cột khi có ANSI; cân nhắc `lipgloss/table` hoặc tự render row |
| 7 | Lưu namespace cuối cùng của mỗi context | Lưu state của app riêng | `~/.config/kboba/state.yaml`, không phải kubeconfig |
| 8 | Cột CPU/MEM | So sánh poll và informer | `k8s.io/metrics`; metrics không có watch nên phải dùng `tea.Tick`; xử lý trường hợp không có metrics-server |
| 9 | CRD qua dynamic client | Discovery API, `unstructured` | `dynamic.Interface`, `dynamicinformer`, `additionalPrinterColumns` |
| 10 | Kiểm tra quyền trước (SelfSubjectAccessReview) | Authorization API | Là verb `create`; cần thêm ngoại lệ có chủ đích cho test read-only và transport |

**Không làm:** exec, port-forward, edit, delete, scale (vi phạm read-only); plugin system và theme config (tốn công nhưng học được ít).

---

## 11. Thứ tự đọc code đề xuất

1. `cmd/kboba/main.go`: điểm vào, flags, cách khởi tạo.
2. `internal/k8s/client.go`: interface `Client`, `NewClient`, `readOnlyTransport`.
3. `internal/ui/root.go`: `Model`, `Update` (phần định tuyến message), `handleKey`, `setActive`, `layout`.
4. `internal/ui/contexts.go` + `listview.go`: sub-model đơn giản nhất.
5. `internal/k8s/pods.go` → `WatchPods`, đọc song song với `internal/ui/pods.go` → `start`, `waitForPodEvents`, `Update`. Đây là phần cốt lõi.
6. `internal/ui/stream.go` → `receiveBatch`.
7. `internal/k8s/logs.go` song song với `internal/ui/logs.go` để thấy lại cùng pattern ở một nguồn dữ liệu khác.
8. `internal/k8s/readonly_test.go` và `internal/ui/root_test.go`: cách các bất biến quan trọng được khóa lại bằng test.
