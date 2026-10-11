# kboba: nhật ký học tập

Tài liệu này ghi lại kboba được xây dựng như thế nào: mỗi phase làm gì, học được gì, và mỗi ý tưởng nằm ở đâu trong code. Mục đích là để sau này đọc lại code thì hiểu được *tại sao* nó được viết như vậy.

> Thuật ngữ kỹ thuật được giữ nguyên tiếng Anh. Code được tham chiếu theo **file + tên symbol** thay vì số dòng, vì số dòng sẽ thay đổi khi code thay đổi. Dùng `grep -n "func (v resourcesView) Update" -r internal/` để nhảy tới đúng chỗ.

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

**Phase 2**

12. [Phase 2.1: Resource views](#12-phase-21-resource-views-deployments-services-events-nodes)
13. [Phase 2.2: Xem YAML](#13-phase-22-xem-yaml-y)
14. [Phase 2.3: Logs nâng cao](#14-phase-23-logs-tìm-kiếm-wrap-timestamps-previous)
15. [Phase 2.4: Drill-down và navigation stack](#15-phase-24-drill-down-và-navigation-stack)
16. [Phase 2.5: Sắp xếp cột](#16-phase-25-sắp-xếp-cột)
17. [Phase 2.6: Tô màu và table tự viết](#17-phase-26-tô-màu-theo-trạng-thái-và-table-tự-viết)
18. [Phase 2.7: Nhớ namespace của mỗi context](#18-phase-27-nhớ-namespace-của-mỗi-context)
19. [Phase 2.8: Cột CPU/MEM](#19-phase-28-cột-cpumem-từ-metrics-server)
20. [Phase 2.9: Mọi resource, kể cả CRD](#20-phase-29-mọi-resource-kể-cả-crd-qua-discovery-và-dynamic-client)
21. [Phase 10: Kiểm tra quyền trước bằng SelfSubjectAccessReview](#21-phase-10-kiểm-tra-quyền-trước-bằng-selfsubjectaccessreview)

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
                      + POST selfsubjectaccessreviews (phase 10, chỉ để hỏi quyền)

internal/state        (phase 2.7) file state riêng của kboba, ~/.config/kboba/state.yaml.
                      KHÔNG import bubbletea; UI dùng nó qua interface NamespaceMemory
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
| `resourcesView`  | `internal/ui/resources.go`  | `textinput` + `tableModel` tự viết (`table.go`, từ phase 2.6; trước đó là bubbles `table`) |
| `logsView`       | `internal/ui/logs.go`       | `viewport`        |
| `detailView`     | `internal/ui/detail.go`     | `viewport` (describe + YAML) |

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

**Transport chỉ cho GET:** `readOnlyTransport` trong `client.go`, được gắn vào bằng `cfg.Wrap(newReadOnlyTransport)`. Mọi request có method khác `GET`/`HEAD` đều bị từ chối trước khi rời khỏi máy. (Từ [phase 10](#21-phase-10-kiểm-tra-quyền-trước-bằng-selfsubjectaccessreview) có đúng một ngoại lệ: `POST .../selfsubjectaccessreviews`.)

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

> **Ghi chú:** ở [phase 2.1](#12-phase-21-resource-views-deployments-services-events-nodes), code của phase này được tổng quát hóa cho nhiều loại resource. Các đoạn code và tên symbol bên dưới dùng **tên hiện tại**. Bảng đổi tên:
>
> | Lúc viết phase (c) | Hiện tại |
> |---|---|
> | `podsView` (`internal/ui/pods.go`) | `resourcesView` (`internal/ui/resources.go`) |
> | `WatchPods(ns)` (`internal/k8s/pods.go`) | `WatchResources(rt, ns)` (`internal/k8s/resources.go`) |
> | `PodEvent`, `PodWatch` | `ResourceEvent`, `ResourceWatch` |
> | `PodUpserted`/`PodDeleted`/`PodsSynced`/`PodWatchFailed` | `Upserted`/`Deleted`/`Synced`/`WatchFailed` |
> | `podWatchStartedMsg`, `podEventsMsg`, `podWatchClosedMsg` | `watchStartedMsg`, `resourceEventsMsg`, `watchClosedMsg` |
>
> Xem code nguyên bản bằng `git show bcbd957`.

### Mục tiêu
Bảng pods tự cập nhật realtime bằng informer, không poll. Đổi context/namespace thì stop informer cũ trước khi tạo cái mới.

### Học được gì
- `SharedInformerFactory`, event handler, `WaitForCacheSync`, `Shutdown`.
- Biến một channel thành dòng message của Bubble Tea (pattern "subscription").
- Tránh race và leak khi cần dừng một nguồn dữ liệu bất đồng bộ.
- Cách `kubectl` tính các cột STATUS, READY, RESTARTS.
- Component `table`: cột co giãn theo chiều rộng, giữ cursor khi dữ liệu thay đổi.

### Thể hiện trong code

#### 4.1 Informer → channel (`internal/k8s/resources.go` → `WatchResources`)

```
factory := informers.NewSharedInformerFactoryWithOptions(cs, 0, informers.WithNamespace(ns))
informer := factory.ForResource(rt.gvr).Informer()   // ban đầu: factory.Core().V1().Pods().Informer()
informer.SetWatchErrorHandler(...)   → ResourceEvent{Type: WatchFailed}
informer.AddEventHandler(Add/Update/Delete) → ResourceEvent{Upserted | Deleted}  (qua rt.convert)
factory.Start(stopCh)
goroutine: WaitForCacheSync → ResourceEvent{Type: Synced}
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

- **`ResourceWatch` là struct có field hàm** (`Events`, `Stop func()`) thay vì có method. Nhờ vậy test UI tự tạo được một `ResourceWatch` giả mà không cần constructor.

#### 4.2 Channel → message của Bubble Tea (`internal/ui/resources.go`)

Pattern "chờ một message rồi đăng ký lại" (subscription):

```go
func waitForResourceEvents(w *k8s.ResourceWatch, gen int) tea.Cmd {
    return func() tea.Msg {
        events, ok := receiveBatch(w.Events, 500)   // block ở goroutine của Cmd, không phải Update
        if !ok {
            return watchClosedMsg{gen: gen}
        }
        return resourceEventsMsg{events: events, gen: gen}
    }
}

// trong Update:
case resourceEventsMsg:
    ... áp dụng events vào v.items ...
    return v, tea.Batch(status, waitForResourceEvents(v.watch, v.gen)) // đăng ký lại
```

Mỗi lần nhận một batch, `Update` trả về một Cmd mới để chờ batch tiếp theo. Vì vậy luôn có **đúng một** Cmd đang chờ trên channel.

**`receiveBatch`** (`internal/ui/stream.go`) chờ 1 phần tử, rồi lấy thêm những phần tử đang có sẵn mà không chờ, tối đa `limit`. Khi informer gửi danh sách ban đầu (có thể hàng trăm pod), app chỉ chạy 1 lần `Update` + render thay vì hàng trăm lần. Hàm generic `[T any]` này được dùng lại cho log stream ở phase (d).

#### 4.3 Generation counter: chống message cũ

`resourcesView` có field `gen int`. Mọi message của watch mang theo `gen`:

```
start():  stop()  →  gen++  →  Cmd(WatchResources) → watchStartedMsg{gen}
Update:   msg.gen != v.gen  →  bỏ qua message
```

Tình huống nó giải quyết: bạn đổi namespace từ `a` sang `b`. Nhưng một `resourceEventsMsg` của `a` đã nằm sẵn trong hàng đợi message của Bubble Tea, nên `stop()` không thu hồi được nó. Nếu không có `gen`, pod của `a` sẽ lọt vào bảng của `b`.

Một trường hợp đặc biệt cần xử lý thêm: nếu `watchStartedMsg` đến với `gen` cũ, tức là người dùng đã đổi namespace *trước khi* watch kịp khởi động, thì phải gọi `msg.watch.Stop()`. Nếu không, informer đó chạy mãi mà không ai dừng.

#### 4.4 Đổi context/namespace

Trong `internal/ui/root.go`:
- `switchNamespace` gọi `m.resources.start(m.client, m.resources.rt, ns)`.
- `handleClientReady` (sau khi đổi context) cũng gọi `m.resources.start(...)`.

Bên trong, `start()` gọi `stop()` trước tiên, nên informer cũ luôn được dừng *trước khi* informer mới được tạo.

Các message của watch (`watchStartedMsg`, `resourceEventsMsg`, `watchClosedMsg`, `ageTickMsg`) luôn được root chuyển cho `m.resources`, **dù view nào đang active**. Nhờ vậy khi bạn đang xem logs, bảng pods vẫn được cập nhật ở phía sau.

#### 4.5 Format giống kubectl (`internal/k8s/pods.go`)

- `NewPodInfo` (vẫn ở `internal/k8s/pods.go`) chuyển `*corev1.Pod` thành `PodInfo`: READY (`ready/total`), RESTARTS (tổng của các container), danh sách container và `DefaultContainer` (đọc từ annotation `kubectl.kubernetes.io/default-container`).
- `podStatus` là bản rút gọn logic của kubectl, theo thứ tự ưu tiên:
  1. `Terminating` (nếu có `DeletionTimestamp`);
  2. init container chưa xong (`Init:Error`, `Init:1/2`…);
  3. lý do waiting/terminated của container (`CrashLoopBackOff`, `OOMKilled`, `ExitCode:2`…);
  4. phase hoặc `Status.Reason`.

Đây là logic thuần nên được test kỹ trong `pods_test.go` → `TestPodStatus`.

#### 4.6 Table (`internal/ui/resources.go`)

- `setColumns`: cột NAME lấy phần chiều rộng còn lại, cột NAMESPACE chỉ hiện khi đang xem all namespaces. Với bubbles `table`, trước khi `SetColumns` phải `SetRows(nil)`, vì table sẽ index `row[i]` theo số cột; đổi từ 5 lên 6 cột mà rows cũ chỉ có 5 cell sẽ gây panic. (Từ phase 2.6 kboba dùng `tableModel` tự viết, vốn chịu được row thiếu cell, nên dòng `SetRows(nil)` đã được bỏ.)
- `setRows(selectedKey)`: rebuild các row từ map, áp dụng filter, sort theo `namespace/name`, và **giữ cursor trên đúng pod** bằng key thay vì index. `rowKeys` song song với rows để tra ngược từ cursor ra `PodInfo`.
- `ageTick`: timer UI 5 giây để cột AGE tự cập nhật. Timer này không gọi cluster. Chỉ có một chuỗi tick, bắt đầu từ `Init`.

#### 4.7 Hạ tầng kind (`Makefile`, `hack/kind/`)

- Cluster kind có kubeconfig riêng (`.kind/admin.kubeconfig`), nên `~/.kube/config` không bị đụng tới. Mọi lệnh `kubectl` trong Makefile đều chỉ định cả `--kubeconfig` và `--context`.
- `rbac.yaml` có hai ServiceAccount:
  - `kboba-readonly`: ClusterRole với get/list/watch;
  - `kboba-limited`: Role chỉ trong `kboba-demo`, dùng để thử lỗi forbidden.
- `readonly-kubeconfig.sh` sinh kubeconfig dùng token của các SA trên, thêm một context trỏ tới endpoint chết để thử lỗi kết nối.

### Test liên quan
- `internal/k8s/pods_test.go`: `TestPodStatus` (bảng case), `TestNewPodInfo`, `TestWatchResourcesPods` trong `resources_test.go` (tạo/xóa pod trên fake clientset, kiểm tra event, kiểm tra `Stop` đóng channel).
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
body, err := c.clientset.CoreV1().Pods(ns).GetLogs(pod, opts).Stream(ctx) // opts: Follow, TailLines=500, Container (+ Timestamps, Previous từ phase 2.3)
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
- View lưu `lines`/`errs` của stream hiện tại vào field, để `Update` đăng ký lại (giống `resourcesView.watch`).
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

**Describe view** (ban đầu là `describeView` trong `internal/ui/describe.go`; từ [phase 2.2](#13-phase-22-xem-yaml-y) trở thành `detailView` trong `internal/ui/detail.go`):
- Message kết quả mang id của request (ban đầu là `describeLoadedMsg.key`, hiện là `detailLoadedMsg.id`); kết quả không khớp với thứ đang xem sẽ bị bỏ qua. Đây là biến thể của generation counter: dùng id thay cho số đếm.
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
- Pods view chỉ hiện `esc clear filter` khi đang có filter (`resourcesView.keys`).

**Layout** (`Model.layout` và `Model.View`): chiều cao body = chiều cao cửa sổ − header − status − help. Body được ép đúng chiều cao (`Height` + `MaxHeight`) để status bar và help bar luôn nằm sát đáy, không bị nhảy lên khi danh sách ngắn.

### Test liên quan
- `internal/k8s/describe_test.go`: nội dung output, events của pod khác bị loại, pod không tồn tại thì trả lỗi, lỗi list events được in ra.
- `internal/ui/root_test.go`: `TestDescribe` (kết quả cũ bị bỏ qua), `TestDescribeDeletedPodShowsError`, `TestQuitKeyIgnoredWhileTyping`.

---

## 7. Các pattern xuyên suốt

| Pattern | Ý tưởng | Ở đâu |
|---|---|---|
| **Không block trong `Update`** | Mọi I/O đều nằm trong một `tea.Cmd` | `connect`, `loadContexts`, `loadNamespaces`, `resourcesView.start`, `logsView.restart`, `detailView.refresh` |
| **Subscription channel → Msg** | Cmd chờ channel, `Update` xử lý xong thì trả về Cmd chờ tiếp | `waitForResourceEvents`, `waitForLogLines` |
| **Batching** | Chờ 1 phần tử, lấy thêm những gì có sẵn | `receiveBatch` (`stream.go`) |
| **Generation / key guard** | Message mang theo "phiên"; phiên cũ thì bỏ qua | `resourcesView.gen`, `logsView.gen`, `detailLoadedMsg.id` |
| **Stop rồi mới close** | Đóng stop channel → chờ producer thoát → đóng data channel | `WatchResources` (closure `stop`), `StreamLogs` (defer) |
| **Một chỗ cleanup** | Rời logs view luôn đi qua `setActive` | `Model.setActive` |
| **Con báo, cha quyết** | View phát message ý định; root đổi state | `contextSelectedMsg`, `namespaceSelectedMsg`, `openLogsMsg`, `openDescribeMsg`, `backMsg` |
| **Lỗi lên status bar** | View trả về `reportErr(err)`, root hiển thị | `messages.go` → `statusMsg`, `reportErr`, `reportInfo` |
| **Domain type thay vì API type** | k8s trả `PodInfo` đã format sẵn; UI không cần hiểu `corev1` | `NewPodInfo` |
| **Read-only 3 lớp** | Allowlist interface + kiểm tra action của fake + transport chỉ GET (ngoại lệ duy nhất: tạo SelfSubjectAccessReview, từ phase 10) | `readonly_test.go` (`isRead`, `allowedCreates`), `readOnlyTransport` |

### Vì sao sub-model dùng value receiver cho `Update`?

`func (v resourcesView) Update(msg) (resourcesView, tea.Cmd)` trả về một bản copy mới, đúng tinh thần Elm (state bất biến). Root gán lại: `m.resources, cmd = m.resources.Update(msg)`. Các helper thay đổi state như `start`, `stop`, `SetSize` dùng pointer receiver và chỉ được gọi trên bản copy cục bộ của root (`m` trong `Update` là biến cục bộ, có thể lấy địa chỉ). Lưu ý: map và slice bên trong vẫn chia sẻ vùng nhớ giữa các bản copy. Điều này ổn vì bản copy cũ luôn bị bỏ đi ngay sau mỗi `Update`.

---

## 8. Chiến lược test

### `internal/k8s`: fake clientset
- `newFakeClient(t, objs...)` (`fake_test.go`) tạo `client` từ `fake.NewSimpleClientset`.
- Fake ghi lại mọi request vào `cs.Actions()`, và đây là nền tảng của `TestClientOnlyReads`.
- Fake **hỗ trợ watch**, nên `TestWatchResourcesPods` chạy informer thật trên fake. Test tạo/xóa pod qua fake để giả lập hoạt động của cluster; đây là thao tác ghi lên *fake*, không phải do kboba thực hiện.
- Fake `GetLogs` trả về body cố định `"fake logs"`.

### `internal/ui`: fake `k8s.Client`
- `fakeClient` trong `root_test.go` implement đủ 7 method:
  - `WatchResources` trả về channel đã nạp sẵn event, và ghi nhận `stopped`;
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
| bubbles table gán `d` cho half page down | (có thể nuốt phím describe) | `resourcesView.handleKey` kiểm tra phím describe trước khi chuyển cho table. `tableModel` (phase 2.6) không gán `d` |
| klog ghi ra stderr | Log của client-go in đè lên TUI | `klog.SetLogger(logr.Discard())` |
| Đóng channel khi handler còn gửi | panic `send on closed channel` | `close(stopCh)` → `Shutdown()` → `close(events)` |
| Không đóng channel events | Cmd chờ bị kẹt mãi, leak goroutine | `Stop` luôn đóng `events` sau khi shutdown |
| Message stale sau khi đổi ns | Pod của namespace cũ lọt vào bảng | Generation counter |
| Watch khởi động sau khi đã đổi ns | Informer mồ côi chạy mãi | Gặp `watchStartedMsg` có `gen` cũ thì gọi `Stop()` |
| `SetColumns` khi rows có ít cell hơn số cột | panic index out of range | `SetRows(nil)` trước `SetColumns` |
| `bufio.Scanner` mặc định 64KB/dòng | Dòng log dài làm stream dừng | `sc.Buffer(..., 1MB)` |
| Context không tồn tại | Mất client hiện tại | `connect` trả lỗi kèm danh sách context; giữ client cũ |
| `Init` trả về `tea.Batch` | Test gửi nhầm `BatchMsg` vào `Update` | `runCmd` mở rộng `BatchMsg` |
| Fake clientset bỏ qua field selector | Events của pod khác lọt vào describe | Lọc lại phía client |
| Pod chưa được schedule | Logs rỗng, không có lỗi | Đúng hành vi của API server (`kubectl logs` cũng vậy) |

---

## 10. Các phase tiếp theo

Phase 2 được làm theo thứ tự dưới đây, mỗi phase là một commit. Mọi tính năng vẫn giữ nguyên tắc read-only. Chi tiết của các phase đã xong nằm ở mục 12 trở đi.

| Phase | Tính năng | Học được gì | Trạng thái |
|---|---|---|---|
| 2.1 | View cho Deployments, Services, Events, Nodes | Tạo abstraction sau khi đã có ví dụ thật; generic informer | ✅ [mục 12](#12-phase-21-resource-views-deployments-services-events-nodes) |
| 2.2 | Xem YAML (`y`) | Serialize object, highlight cú pháp | ✅ [mục 13](#13-phase-22-xem-yaml-y) |
| 2.3 | Tìm kiếm trong log, wrap, timestamps, `--previous` | Thao tác trên ring buffer, highlight | ✅ [mục 14](#14-phase-23-logs-tìm-kiếm-wrap-timestamps-previous) |
| 2.4 | Drill-down Deployment → Pods | Navigation stack trong Elm, label selector | ✅ [mục 15](#15-phase-24-drill-down-và-navigation-stack) |
| 2.5 | Sắp xếp cột | Logic thuần, dễ test | ✅ [mục 16](#16-phase-25-sắp-xếp-cột) |
| 2.6 | Tô màu theo status | Giới hạn của component có sẵn | ✅ [mục 17](#17-phase-26-tô-màu-theo-trạng-thái-và-table-tự-viết) |
| 2.7 | Lưu namespace cuối cùng của mỗi context | Lưu state của app riêng | ✅ [mục 18](#18-phase-27-nhớ-namespace-của-mỗi-context) |
| 2.8 | Cột CPU/MEM | So sánh poll và informer | ✅ [mục 19](#19-phase-28-cột-cpumem-từ-metrics-server) |
| 2.9 | CRD qua dynamic client | Discovery API, `unstructured` | ✅ [mục 20](#20-phase-29-mọi-resource-kể-cả-crd-qua-discovery-và-dynamic-client) |

| 10 | Kiểm tra quyền trước (SelfSubjectAccessReview) | Authorization API; nới lỏng guard một cách có kiểm soát | ✅ [mục 21](#21-phase-10-kiểm-tra-quyền-trước-bằng-selfsubjectaccessreview) |

**Không làm:**
- exec, port-forward, edit, delete, scale: vi phạm read-only.
- Plugin system, theme config: tốn công nhưng học được ít.

## 11. Thứ tự đọc code đề xuất

1. `cmd/kboba/main.go`: điểm vào, flags, cách khởi tạo.
2. `internal/k8s/client.go`: interface `Client`, `NewClient`, `readOnlyTransport`.
3. `internal/ui/root.go`: `Model`, `Update` (phần định tuyến message), `handleKey`, `setActive`, `layout`.
4. `internal/ui/contexts.go` + `listview.go`: sub-model đơn giản nhất.
5. `internal/k8s/resources.go` → `WatchResources`, đọc song song với `internal/ui/resources.go` → `start`, `waitForResourceEvents`, `Update`. Đây là phần cốt lõi.
6. `internal/ui/stream.go` → `receiveBatch`.
7. `internal/k8s/logs.go` song song với `internal/ui/logs.go` để thấy lại cùng pattern ở một nguồn dữ liệu khác.
8. `internal/k8s/readonly_test.go` và `internal/ui/root_test.go`: cách các bất biến quan trọng được khóa lại bằng test.

---

# Phase 2

## 12. Phase 2.1: Resource views (Deployments, Services, Events, Nodes)

### Mục tiêu
Thêm `:deploy`, `:svc`, `:events`, `:nodes` bên cạnh `:pods`, dùng chung **một** bảng live và **một** hàm watch, thay vì nhân bản code của pods cho từng loại resource.

### Học được gì
- **Tạo abstraction khi đã có ví dụ thật.** `podsView` đã chạy ổn từ phase (c), nên lúc này ta biết chính xác phần nào là chung (informer, gen, filter, table, cursor, AGE) và phần nào thay đổi theo loại resource (cột, cách biến object thành row).
- **Generic informer:** `factory.ForResource(gvr)` trả về informer cho bất kỳ resource built-in nào, chỉ cần `GroupVersionResource`.
- **Generics của Go cho type assertion:** hàm `typed[T]` biến một converter có kiểu cụ thể thành converter nhận `any`.
- **Resource cluster-scoped và namespaced:** nodes bỏ qua namespace và không có cột NAMESPACE.

### Thể hiện trong code

**Mô tả một loại resource bằng dữ liệu** (`internal/k8s/resources.go` → `ResourceType`):

```go
Deployments = &ResourceType{
    Name: "deployments", Title: "Deployments", Aliases: []string{"deployment", "deploy", "dp"}, Namespaced: true,
    Columns: []Column{{"NAME", 0}, {"READY", 9}, {"UP-TO-DATE", 10}, {"AVAILABLE", 9}},
    gvr:     appsv1.SchemeGroupVersion.WithResource("deployments"),
    convert: typed(deploymentResource),
}
```

- Muốn thêm một loại resource mới chỉ cần thêm **một giá trị** `ResourceType` và một hàm convert. UI không phải sửa gì.
- `Columns` chỉ chứa các cột riêng của loại đó. UI tự thêm NAMESPACE (khi xem all namespaces và resource là namespaced) và AGE. Cột có `Width 0` là cột co giãn: thường là NAME, còn với events là MESSAGE.
- `gvr` và `convert` là field **unexported**: UI không thể (và không cần) biết client-go được gọi thế nào. Fake client trong test UI chỉ dùng `Name`.
- `ResourceTypes()` và `LookupResourceType(alias)` là registry; lệnh `:deploy`, `:svc`, ... được giải quyết qua `LookupResourceType` trong `Model.runCommand`.

**Một row chung cho mọi loại** (`Resource`): `Namespace`, `Name`, `Created` (dùng cho AGE; với event là "last seen"), `Cells` (một cell cho mỗi cột). Riêng pod có thêm `Pod *PodInfo`, vì logs view và describe view cần danh sách container.

**Converter** (`podResource`, `deploymentResource`, `serviceResource`, `eventResource`, `nodeResource`):
- Đây là các hàm thuần, nhận object có kiểu và trả về `Resource`.
- Hàm `newResource(meta, cells...)` điền các phần chung từ `metav1.Object`.
- Format giống kubectl: `servicePorts` tạo chuỗi `443:30443/TCP`; `serviceExternalIP` trả `<pending>` cho LoadBalancer chưa có IP; `nodeRoles` đọc các label `node-role.kubernetes.io/*`.

**`typed[T]`:**

```go
func typed[T any](f func(T) Resource) func(any) (Resource, bool) {
    return func(obj any) (Resource, bool) {
        o, ok := obj.(T)
        if !ok {
            return Resource{}, false
        }
        return f(o), true
    }
}
```

Informer gọi handler với `any`. `typed` gom phần type assertion lặp lại vào một chỗ; nếu object sai kiểu thì trả `false` thay vì panic.

**Watch generic** (`WatchResources`): giống hệt `WatchPods` cũ, chỉ khác ở hai dòng:

```go
generic, err := factory.ForResource(rt.gvr)     // thay cho factory.Core().V1().Pods()
...
sendObj := func(typ ResourceEventType, obj any) {
    if r, ok := rt.convert(obj); ok { send(ResourceEvent{Type: typ, Resource: r}) }
}
```

Mọi thứ đã học ở phase (c), như thứ tự dừng, tombstone, `SetWatchErrorHandler` và `send` có select trên `stopCh`, được giữ nguyên và giờ áp dụng cho mọi loại resource.

**Phía UI** (`internal/ui/resources.go` → `resourcesView`):
- `start(client, rt, namespace)` thay cho `start(client, namespace)`. Đổi loại resource cũng giống đổi namespace: stop → `gen++` → watch mới. Filter bị xóa khi đổi loại, vì filter tên pod hiếm khi có nghĩa với nodes.
- `setColumns` ghép `[NAMESPACE] + rt.Columns + AGE`, rồi tìm cột `Width 0` để chia phần chiều rộng còn lại.
- `setRows` ghép `[r.Namespace] + r.Cells + age`. Phần này không còn biết gì về pod.
- Hành động riêng của pod (Enter để xem logs, `d` để describe) chỉ hoạt động khi `v.rt == k8s.Pods`, và help bar chỉ hiện các phím đó khi đang ở pods (`resourcesView.keys`).

**Root** (`internal/ui/root.go`):
- `showResources(rt)` chỉ khởi động watch mới khi loại resource thay đổi; nếu không, nó chỉ chuyển view.
- `switchNamespace` và `handleClientReady` giữ nguyên loại resource đang xem (`m.resources.rt`). Vì vậy `:ns all` khi đang ở deployments sẽ cho deployments của mọi namespace.

**Interface `k8s.Client`:** `WatchPods` được thay bằng `WatchResources`. Allowlist trong `readonly_test.go` được cập nhật, và `TestClientOnlyReads` chạy `WatchResources` cho **mọi** `ResourceType`. Thêm một loại resource mới vào registry thì nó tự động được kiểm tra read-only.

**RBAC kind** (`hack/kind/rbac.yaml`): ClusterRole thêm `services`, `nodes` và `apps/deployments, replicasets` (vẫn chỉ get/list/watch). Role của `kboba-limited` không có `nodes`, nên `:nodes` với context này sẽ thấy lỗi forbidden. `demo.yaml` thêm Deployment `web` và Service `web`.

### Test liên quan
- `internal/k8s/resources_test.go`:
  - `TestEveryTypeHasMatchingCells`: số cell khớp số cột cho **mọi** type, nhờ đó tránh được panic của table;
  - `TestDeploymentCells`, `TestServiceCells`, `TestNodeCells`, `TestEventCellsUseLastSeen`: các converter;
  - `TestWatchResourcesPods`, `TestWatchResourcesClusterScopedIgnoresNamespace`: watch generic;
  - `TestLookupResourceType`: tra theo tên và alias.
- `internal/ui/root_test.go`:
  - `TestSwitchResourceType`: watch cũ bị stop; Enter trên deployment không mở logs; `:ns all` giữ nguyên type;
  - `TestClusterScopedHasNoNamespaceColumn`;
  - `TestUnknownResourceCommand`.

### Bẫy
| Bẫy | Cách xử lý |
|---|---|
| Số cell khác số cột làm table panic | `TestEveryTypeHasMatchingCells` khóa lại cho mọi type |
| Nodes là cluster-scoped, `WithNamespace("x")` sẽ không trả về gì | `WatchResources` ép `namespace = ""` khi `!rt.Namespaced`; UI không hiện cột NAMESPACE |
| AGE của event theo `CreationTimestamp` gây hiểu nhầm (event được gộp và lặp lại) | `eventResource` đặt `Created = eventTime(ev)` (last seen) |

## 13. Phase 2.2: Xem YAML (`y`)

### Mục tiêu
Bấm `y` trên bất kỳ dòng nào của bất kỳ bảng nào (pods, deployments, services, events, nodes) để xem manifest dạng YAML có tô màu, đã bỏ `managedFields`.

### Học được gì
- **TypeMeta trống:** object mà typed client trả về có `apiVersion` và `kind` rỗng (client-go dựa vào Go type thay cho TypeMeta). Muốn YAML giống `kubectl get -o yaml` thì phải tự điền lại.
- **Không sửa object của informer cache:** object trong cache được chia sẻ giữa các consumer. Ở đây ta `Get` một bản mới từ API rồi mới sửa (bỏ managedFields).
- **"Lần dùng thứ hai thì tổng quát hóa":** describe và YAML đều là "lấy text, hiện trong viewport, cho refresh". Nên `describeView` được đổi thành `detailView`, nhận một *loader function*.
- **Highlight cú pháp** bằng chroma, và chọn theme theo màu nền terminal.
- **Cuộn ngang trong viewport:** mặc định bước cuộn ngang là 0, phải gọi `SetHorizontalStep`.

### Thể hiện trong code

**Lấy object theo từng loại** (`internal/k8s/resources.go`): `ResourceType` có thêm hai field unexported:
- `kind` (ví dụ `"Deployment"`);
- `get func(ctx, cs, ns, name) (runtime.Object, error)`: mỗi type dùng typed client tương ứng, ví dụ `cs.AppsV1().Deployments(ns).Get(...)`.

Thêm một loại resource vẫn chỉ là thêm một giá trị vào registry.

**Render YAML** (`internal/k8s/yaml.go`):

```go
func toYAML(rt *ResourceType, obj runtime.Object) (string, error) {
    obj.GetObjectKind().SetGroupVersionKind(rt.gvr.GroupVersion().WithKind(rt.kind)) // điền lại apiVersion/kind
    if m, err := meta.Accessor(obj); err == nil {
        m.SetManagedFields(nil)                                                   // bỏ phần dài, ít giá trị
    }
    out, err := yaml.Marshal(obj)                                                 // sigs.k8s.io/yaml: JSON tags → YAML
    ...
}
```

- `meta.Accessor` cho phép thao tác metadata của *bất kỳ* object nào mà không cần biết kiểu cụ thể.
- `sigs.k8s.io/yaml` marshal qua JSON, nên dùng đúng các tag `json:"..."` của Kubernetes, cho ra key `camelCase` giống kubectl.
- `GetYAML` bỏ qua namespace với type cluster-scoped, giống `WatchResources`.

**Một view cho mọi text snapshot** (`internal/ui/detail.go` → `detailView`):

```go
type detailLoader func(ctx context.Context) (string, error)

func (v *detailView) open(title, id string, wrap bool, load detailLoader) tea.Cmd
func (v *detailView) openDescribe(c k8s.Client, pod k8s.PodInfo) tea.Cmd   // wrap = true
func (v *detailView) openYAML(c k8s.Client, rt *k8s.ResourceType, r k8s.Resource) tea.Cmd // wrap = false
```

- View không biết nó đang hiện describe hay YAML: nó chỉ giữ một `load` closure và gọi lại khi bấm `r`.
- `id` (ví dụ `"yaml:deployments:team-a/api"`) thay cho `key` của describe cũ; mục đích vẫn là chặn kết quả trễ.
- `wrap`: describe thì wrap dòng dài. YAML thì **không** wrap (wrap làm hỏng thụt lề, mà thụt lề chính là cú pháp của YAML), thay vào đó cuộn ngang bằng ←/→ (`vp.SetHorizontalStep(4)`).
- `highlightYAML` chạy *bên trong loader*, tức là trong goroutine của Cmd, nên việc highlight một manifest lớn không làm chậm `Update`. Nếu chroma lỗi thì trả về text gốc, vì tô màu không phải lý do để báo lỗi.
- Theme: `lipgloss.HasDarkBackground()` → `monokai` hoặc `github`. lipgloss chỉ hỏi terminal một lần (lần render đầu) rồi cache lại, nên gọi hàm này trong Cmd không tranh stdin với Bubble Tea.
- Viewport cắt dòng dài theo độ rộng hiển thị và **hiểu ANSI** (`ansi.Cut` trong `visibleLines`), nên text có màu vẫn cuộn ngang đúng.

**Phím `y`** (`resourcesView.handleKey`): hoạt động với mọi type, phát `openYAMLMsg{rt, resource}`; root gọi `m.detail.openYAML(...)`.

**Interface:** thêm `GetYAML(ctx, rt, namespace, name)`. Đây là verb `get`, có trong allowlist, và `TestClientOnlyReads` gọi nó cho mọi `ResourceType`.

### Test liên quan
- `internal/k8s/yaml_test.go`:
  - `TestGetYAML`: có `apiVersion`/`kind`, `managedFields` đã bị bỏ;
  - `TestGetYAMLClusterScoped`;
  - `TestGetYAMLAppsGroup`: `apiVersion: apps/v1`;
  - `TestGetYAMLNotFound`.
- `internal/ui/root_test.go` → `TestYAMLForAnyResourceType`: `y` trên deployment, nội dung (sau `ansi.Strip`), không wrap, Esc quay lại đúng bảng deployments.

### Bẫy
| Bẫy | Cách xử lý |
|---|---|
| YAML thiếu `apiVersion`/`kind` | `SetGroupVersionKind` trước khi marshal |
| Sửa object trong cache của informer làm hỏng dữ liệu của consumer khác | Luôn `Get` bản mới từ API |
| Wrap làm hỏng thụt lề YAML | `wrap=false` + cuộn ngang |
| Viewport mặc định không cuộn ngang | `SetHorizontalStep(4)` |

## 14. Phase 2.3: Logs: tìm kiếm, wrap, timestamps, previous

### Mục tiêu
Logs view dùng được khi debug thật:
- `/` để tìm kiếm (không phân biệt hoa thường), highlight kết quả, `n`/`N` để nhảy giữa các kết quả;
- `w` để bật/tắt wrap dòng dài, ←/→ để cuộn ngang khi không wrap;
- `t` để bật/tắt timestamps;
- `p` để xem log của container *trước khi crash* (`kubectl logs --previous`), rất cần cho pod CrashLoopBackOff.

### Học được gì
- **`PodLogOptions`**: `Timestamps`, `Previous`. Khi `Previous` thì không `Follow`, vì container đó sẽ không bao giờ ghi thêm.
- **Chỉ số ổn định trên ring buffer:** khi buffer xoay vòng, "dòng thứ i" trỏ sang dòng khác. Cần một số tuyệt đối (`dropped + i`) để ghi nhớ kết quả tìm kiếm đang đứng.
- **Ánh xạ dòng log sang dòng hiển thị:** khi wrap, một dòng log chiếm nhiều dòng trong viewport. Muốn nhảy đến dòng log thứ i thì cần biết nó *bắt đầu* ở dòng hiển thị nào.
- **Tách logic thuần khỏi view:** tìm kiếm, highlight và chọn kết quả kế tiếp là các hàm thuần, test được bằng bảng case mà không cần Bubble Tea.
- **Esc nhiều tầng:** Esc đầu tiên xóa search, Esc tiếp theo mới rời view.

### Thể hiện trong code

**Tùy chọn log** (`internal/k8s/logs.go`): `StreamLogs(ctx, ns, pod, container, opts LogOptions)`. `LogOptions` là struct của kboba, không phải `corev1.PodLogOptions`, nên UI không phụ thuộc vào kiểu của client-go và chỉ lộ ra đúng những gì người dùng được chỉnh:

```go
opts := &corev1.PodLogOptions{
    Container:  container,
    Follow:     !o.Previous,
    TailLines:  &tail,
    Timestamps: o.Timestamps,
    Previous:   o.Previous,
}
```

`logsView.opts` lưu lựa chọn hiện tại. `t`/`p` đảo cờ rồi gọi `restart()`, tức là quy trình cancel → `gen++` → stream mới đã có từ phase (d). Đổi container (`c`) giữ nguyên `opts`.

**Số dòng tuyệt đối** (`internal/ui/logbuffer.go`): `ringBuffer.dropped` đếm số dòng đã bị đẩy ra. Dòng thứ `i` trong buffer có số tuyệt đối `dropped + i`, và số này không đổi khi buffer tiếp tục xoay. `matches` và `current` của search lưu số tuyệt đối.

**Logic tìm kiếm thuần** (`internal/ui/logsearch.go`):
- `containsFold(line, term)`: so khớp không phân biệt hoa thường.
- `highlightMatches(line, term, style)`: tìm trên `strings.ToLower(line)` rồi cắt `line` gốc theo cùng offset. Nếu `ToLower` làm thay đổi độ dài byte (một vài ký tự Unicode hiếm) thì offset không còn khớp; khi đó chỉ báo match mà không tô màu, thay vì cắt sai chuỗi.
- `nextMatch(matches, current, from, forward)`: chọn kết quả kế tiếp hoặc trước đó và quay vòng. Nếu chưa có `current` thì bắt đầu từ dòng đang ở đầu màn hình (`from`). Nếu `current` đã bị đẩy khỏi buffer thì chọn kết quả gần nó nhất.

**`render()`** (`internal/ui/logs.go`) chạy một lần cho mỗi batch và làm 4 việc trong một vòng lặp:

```go
for i := range v.buf.len() {
    line := strings.ReplaceAll(v.buf.at(i), "\t", "    ")       // tab làm lệch độ rộng
    if containsFold(line, v.term) {
        v.matches = append(v.matches, v.buf.dropped+i)          // 1. tìm kết quả (số tuyệt đối)
        line = highlightMatches(line, v.term, matchStyle)       // 2. tô màu
    }
    if v.wrap && v.viewport.Width > 0 {
        line = ansi.Hardwrap(line, v.viewport.Width, true)      // 3. wrap, hiểu ANSI
    }
    v.lineOffsets = append(v.lineOffsets, row)                  // 4. dòng log i bắt đầu ở hàng nào
    row += strings.Count(line, "\n") + 1
    ...
}
```

- `ansi.Hardwrap` (từ `charmbracelet/x/ansi`) wrap theo độ rộng hiển thị và **không cắt giữa escape code**, nên phần highlight không bị vỡ khi wrap.
- **Nhảy tới kết quả** (`jump`): `v.viewport.SetYOffset(v.lineOffsets[m - v.buf.dropped])`, rồi tắt follow để log mới không kéo màn hình đi.
- **Ngược lại** (`topLine`): `sort.SearchInts(lineOffsets, YOffset+1) - 1` cho ra dòng log đang ở đầu màn hình. `lineOffsets` tăng dần nên tìm nhị phân được.
- Khi wrap, `SetSize` phải `render()` lại vì kết quả wrap phụ thuộc chiều rộng.

**Phím và ô nhập** (`handleKey`, `handleSearchKey`):
- Khi ô search đang focus, `capturingInput()` trả `true`, nên root đẩy mọi phím vào view (gõ `q` không thoát app). Đây là cơ chế đã có từ phase (e), giờ được dùng lại.
- Esc khi đang gõ thì hủy search. Esc khi có `term` thì xóa search. Esc khi không có gì thì `goBack`.
- `n`/`N` chỉ bắt khi có `term`; nếu không có, phím đi tiếp xuống viewport.
- Help bar chỉ hiện `n`/`N` khi đang có search, chỉ hiện `←/→` khi không wrap, và chỉ hiện `c` khi pod có nhiều container.

**Thanh tiêu đề** hiện trạng thái: `follow:on wrap:off ts:on PREVIOUS lines:812 /error 3/17`.

### Test liên quan
- `internal/k8s/logs_test.go` → `TestStreamLogsOptions`: đọc `PodLogOptions` mà fake clientset ghi lại (`GenericAction.GetValue()`) để kiểm tra `Follow`/`Timestamps`/`Previous`/`Container`.
- `internal/ui/logsearch_test.go`: `TestHighlightMatches` (dùng `lipgloss.Style.Transform` để thấy được kết quả mà không cần terminal), `TestContainsFold`, `TestNextMatch` (bảng case: quay vòng, `current` đã bị đẩy ra, ...).
- `internal/ui/logbuffer_test.go`: `dropped` sau khi buffer xoay vòng.
- `internal/ui/root_test.go`:
  - `TestLogsSearch`: gõ `/Error`, các kết quả, `n`/`N` quay vòng, tiêu đề `2/2`, Esc hai tầng;
  - `TestLogsOptionsRestartStream`: `t`/`p` restart stream với đúng options, stream cũ bị cancel, đổi container giữ nguyên options;
  - `TestLogsWrap`: dòng 250 ký tự ở width 100 thành 3 hàng, `lineOffsets` đúng.

### Bẫy
| Bẫy | Cách xử lý |
|---|---|
| Index của kết quả tìm kiếm trỏ sai dòng sau khi buffer xoay vòng | Lưu số tuyệt đối `dropped + i` |
| Wrap làm lệch phép tính "dòng log i ở hàng nào" | `lineOffsets` tính trong cùng vòng lặp với wrap |
| Wrap thủ công cắt đôi escape code của phần highlight | `ansi.Hardwrap` hiểu ANSI |
| `ToLower` làm đổi độ dài byte nên offset highlight sai | Phát hiện `len(lower) != len(line)` thì bỏ qua highlight |
| Tab hiển thị với độ rộng không xác định | Đổi `\t` thành 4 dấu cách khi render |
| `--previous` với `Follow: true` | `Follow: !Previous` |
| Gõ `q`/`n` vào ô search bị hiểu thành phím tắt | `capturingInput()` khi ô search đang focus |

## 15. Phase 2.4: Drill-down và navigation stack

### Mục tiêu
Enter trên một Deployment hoặc Service sẽ mở danh sách **pods do nó chọn**. Esc quay lại đúng bảng cũ, với đúng dòng đang chọn trước đó, giống k9s. Từ danh sách pods đó vẫn mở được logs, describe, YAML; Esc từ các view đó quay về danh sách pods đã lọc chứ không về bảng gốc.

### Học được gì
- **Label selector:** `metav1.LabelSelector` (matchLabels + matchExpressions) được chuyển thành chuỗi `app=web,tier in (edge,front)`. Selector được gửi lên API server để server lọc trên cả list lẫn watch.
- **Tinh chỉnh informer:** dùng `informers.WithTweakListOptions` để thêm `LabelSelector` vào mọi request của informer.
- **Navigation stack trong Elm:** lịch sử chỉ là một slice dữ liệu trong model. "Quay lại" nghĩa là lấy entry cuối ra rồi chạy lại đúng query đó.
- **Gom state thành một giá trị:** những gì bảng đang hiển thị (`resourceQuery`) được gom vào một struct, nên lưu và khôi phục state chỉ là copy một giá trị.
- **Selector rỗng là nguy hiểm:** selector `""` nghĩa là "khớp tất cả", không phải "không khớp gì".

### Thể hiện trong code

**Selector của object** (`internal/k8s/resources.go`): `Resource.Selector`.
- Deployment: `metav1.LabelSelectorAsSelector(d.Spec.Selector)`, xử lý được cả `matchExpressions`.
- Service: `labels.SelectorFromSet(s.Spec.Selector)`.
- Nếu không có selector thì để chuỗi **rỗng**, và UI sẽ không cho drill-down. Nếu không chặn, một selector rỗng sẽ liệt kê *mọi* pod trong namespace.

**Watch có selector:** `WatchResources(rt, namespace, labelSelector)`:

```go
factory := informers.NewSharedInformerFactoryWithOptions(c.clientset, 0,
    informers.WithNamespace(namespace),
    informers.WithTweakListOptions(func(o *metav1.ListOptions) { o.LabelSelector = labelSelector }),
)
```

Server lọc giúp ta, nên client chỉ nhận đúng các pod cần thiết, kể cả trong các event watch sau đó.

**Gom query thành struct** (`internal/ui/resources.go`):

```go
type resourceQuery struct {
    rt        *k8s.ResourceType
    namespace string
    selector  string // "" = không lọc
    scope     string // "deployments/web", hiện trên tiêu đề
}
```

`resourcesView.start(client, q, selectKey)` thay cho `start(client, rt, namespace)`. Mọi chỗ trước đây đọc `v.rt`/`v.namespace` giờ đọc `v.q.rt`/`v.q.namespace`.

**Navigation stack** (`internal/ui/root.go`):

```go
type navEntry struct {
    query    resourceQuery // bảng đang hiển thị gì
    selected string        // dòng nào đang được chọn
}
// Model.nav []navEntry
```

- `drillDown`: đẩy `{m.resources.q, selectedKey}` vào stack, rồi `start` một query pods với `namespace = namespace của deployment` (kể cả khi đang xem all namespaces) và `selector = resource.Selector`.
- `popNav`: lấy entry cuối ra và `start(entry.query, entry.selected)`.
- **Esc trong bảng** (không có filter) giờ phát `goBack`. Root nhận `backMsg` khi đang ở `viewResources` thì gọi `popNav`; ở các view khác (logs, detail, contexts, namespaces) thì quay về bảng như trước. Vì vậy từ logs Esc về bảng pods đã lọc, Esc thêm lần nữa mới về deployments.
- **Điều hướng tường minh sẽ xóa lịch sử:** `:pods`, `:deploy`, `:ns ...`, đổi context đều đặt `m.nav = nil` và bắt đầu query mới không có selector.
- `m.namespace` (namespace của app, hiện trên header) **không đổi** khi drill-down. Namespace của query nằm riêng trong `resourceQuery`.

**Khôi phục dòng đã chọn** (`pendingSelect`): khi quay lại, watch được khởi động lại nên các dòng đến dần theo event. `start(..., selectKey)` đặt `v.pendingSelect`; `setRows` ưu tiên key này cho tới khi dòng đó xuất hiện, rồi xóa nó. Nếu đã `Synced` mà vẫn không thấy (object đã bị xóa) thì cũng xóa, để con trỏ không nhảy bất ngờ về sau.

**Phím Enter có nghĩa theo loại resource** (`resourcesView.handleKey`, `keys`): với pods là "logs", với deployments/services là "pods", còn với các loại khác thì không làm gì. Help bar hiện đúng nhãn tương ứng.

### Test liên quan
- `internal/k8s/resources_test.go`:
  - `TestSelectors`: matchExpressions, service selector, và **selector rỗng phải là chuỗi rỗng**;
  - `TestWatchResourcesLabelSelector`: danh sách ban đầu đã được lọc; `ListAction.GetListRestrictions()` và `WatchAction.GetWatchRestrictions()` cho thấy selector thực sự được gửi đi.
- `internal/ui/root_test.go`:
  - `TestDrillDownAndBack`: drill ở chế độ all namespaces; watch dùng namespace của deployment và đúng selector; tiêu đề có `← deployments/worker`; logs → Esc vẫn giữ bộ lọc; Esc lần nữa về deployments với dòng `worker` được chọn lại; stack rỗng thì Esc không làm gì;
  - `TestDrillDownNeedsSelector`: object không có selector thì không drill;
  - `TestCommandResetsNavigation`.
- Fake client dùng `labels.Parse(selector).Matches(...)` để lọc pod giống API server.

### Bẫy
| Bẫy | Cách xử lý |
|---|---|
| Selector rỗng khớp **mọi** pod | `Selector` để rỗng khi không có selector, và UI chặn drill-down |
| Drill-down ở all namespaces sẽ lấy pods cùng label ở namespace khác | Query dùng namespace của deployment |
| Quay lại thì con trỏ về dòng 0, vì watch khởi động lại | `pendingSelect` |
| Esc từ logs nhảy thẳng về bảng gốc | Esc trong logs/detail chỉ về bảng; chỉ Esc *trong bảng* mới pop stack |
| `:pods` khi đang ở pods đã lọc không làm gì (cùng `rt`) | `showResources` cũng restart khi `len(m.nav) > 0` |

## 16. Phase 2.5: Sắp xếp cột

### Mục tiêu
Sắp xếp được mọi bảng theo bất kỳ cột nào:
- `s` để chuyển sang cột kế tiếp (sau cột cuối thì quay về thứ tự mặc định);
- `S` để đảo chiều;
- tiêu đề của cột đang sắp xếp có mũi tên `↑`/`↓`.

### Học được gì
- **Natural sort:** so sánh chuỗi thông thường cho `"10" < "9"` và `"pod-10" < "pod-2"`. Natural sort so sánh các đoạn chữ số như số thật.
- **Sắp xếp theo giá trị gốc, không theo text hiển thị:** cột AGE hiện `5m`, `3h4m`, nên so sánh text là sai. Phải so sánh `Created`.
- **Thứ tự ổn định:** dữ liệu từ informer đến liên tục; nếu các dòng bằng nhau không có thứ tự cố định, bảng sẽ "nhảy" mỗi lần có event. Luôn phá thế hòa bằng key.
- **Định danh cột bằng tiêu đề thay vì index:** index thay đổi khi cột NAMESPACE xuất hiện hoặc biến mất; tiêu đề thì không.

### Thể hiện trong code

**So sánh natural** (`internal/ui/sort.go` → `naturalLess`): duyệt hai chuỗi song song. Khi cả hai đang ở một đoạn chữ số, `splitDigits` tách đoạn đó ra và `compareNumbers` so sánh: bỏ số 0 ở đầu, đoạn nào dài hơn thì lớn hơn, cùng độ dài thì so từng ký tự. Cách này **không chuyển sang `int`**, nên số rất lớn không bị tràn. Ngoài các đoạn số, so sánh từng rune không phân biệt hoa thường. Nhờ vậy:
- READY `1/3 < 2/3`;
- RESTARTS `9 < 10`;
- NAME `web-2 < web-10`.

**Sắp xếp các dòng** (`internal/ui/resources.go`):
- `setRows` dựng `[]rowEntry{key, res, row}` (dòng đã render cùng `Resource` gốc), rồi gọi `sortEntries(entries, col, byAge, desc)` *trước khi* tách ra `rowKeys` và `rows`. Hai slice này luôn khớp nhau, nên việc tra ngược từ cursor ra object vẫn đúng.
- `sortEntries`:

  ```go
  switch {
  case byAge: less, greater = a.res.Created.After(b.res.Created), ...   // trẻ nhất trước
  case col >= 0: less, greater = naturalLess(a.row[col], b.row[col]), ...
  default: less, greater = a.key < b.key, ...                             // namespace rồi name
  }
  if !less && !greater { return a.key < b.key }                           // hòa → theo key, kể cả khi đảo chiều
  if desc { return greater }
  return less
  ```

  Khi đảo chiều, chỉ đảo phép so sánh chính; phần phá thế hòa vẫn tăng dần theo key, nên thứ tự luôn xác định.

**Cột sắp xếp lưu bằng tiêu đề** (`sortCol string`):
- `columns()` trả về `[NAMESPACE] + rt.Columns + AGE`. Chính hàm này được dùng cho cả `setColumns` (header) lẫn `columnTitles()` (tìm vị trí cột), nên hai nơi không thể lệch nhau.
- `slices.Index(titles, sortCol)` cho ra index; nếu cột không còn tồn tại (ví dụ NAMESPACE sau khi rời "all") thì index là `-1`, tức là thứ tự mặc định.
- `nextSortColumn` xoay vòng `NAME → … → AGE → "" (mặc định) → NAME`.
- Đổi loại resource thì reset `sortCol`/`sortDesc`, vì cột của loại khác hoàn toàn khác.

**Header có mũi tên:** `setColumns` nối `↑`/`↓` vào tiêu đề và nới cột cố định nếu cần (`max(width, lipgloss.Width(title))`). Nếu không, bubbles table sẽ cắt tiêu đề thành `RESTARTS…` và mất mũi tên.

### Test liên quan
- `internal/ui/sort_test.go`:
  - `TestNaturalLess`: số, `1/3`, phân biệt hoa thường, tiền tố, `007` bằng `7`, số 21 chữ số;
  - `TestSortEntries`: thứ tự mặc định, cột số, AGE, đảo chiều; thế hòa vẫn theo key khi đảo chiều;
  - `TestNextSortColumn`: xoay vòng, và cột đã biến mất.
- `internal/ui/root_test.go` → `TestSortKeys`: `s`/`S`, header `NAME↑`, sắp theo READY, đổi type thì reset.

### Bẫy
| Bẫy | Cách xử lý |
|---|---|
| `"10" < "9"` khi so sánh chuỗi | `naturalLess` |
| Sắp AGE theo text `"5m"` | So sánh `Created` |
| Bảng nhảy dòng mỗi khi informer gửi event | Phá thế hòa bằng key |
| Index cột thay đổi khi có hoặc mất cột NAMESPACE | Lưu cột bằng tiêu đề |
| Mũi tên trong header bị cắt | Nới độ rộng cột |
| Gửi nhiều phím quá nhanh (ví dụ qua `tmux send-keys "s s S"`) bị Bubble Tea gộp thành một `KeyMsg` nhiều rune, nên `key.Matches` không khớp | Không phải lỗi của app: người gõ phím bình thường không gặp. Khi test bằng tmux, gửi từng phím một |

## 17. Phase 2.6: Tô màu theo trạng thái (và table tự viết)

### Mục tiêu
Thấy ngay vấn đề khi nhìn vào bảng, theo bốn mức:

| Mức | Màu | Ví dụ |
|---|---|---|
| Lỗi | đỏ | CrashLoopBackOff, ImagePullBackOff, Error, OOMKilled, node NotReady |
| Đang chờ | vàng | Pending, ContainerCreating, Terminating, `Init:0/1`; pod Running nhưng READY chưa đủ; deployment đang rollout; event Warning |
| Đã xong | xám | Completed, Succeeded |
| Bình thường | màu mặc định | mọi trường hợp còn lại |

### Học được gì
- **Giới hạn của component có sẵn**, và khi nào nên tự viết. bubbles `table` không dùng được với cell có màu vì hai lý do:
  1. `renderRow` cắt chuỗi bằng `runewidth.Truncate`, hàm này đếm cả các byte của escape code ANSI là độ rộng, nên cột bị lệch hoặc mã màu bị cắt dở.
  2. Dòng đang chọn được bọc *bên ngoài* bằng `Selected.Render(row)`; mã reset ở cuối mỗi cell có màu sẽ xóa luôn nền highlight cho phần còn lại của dòng.
- **Cách viết một component Bubble Tea:** một struct có `Update(msg) (model, cmd)` và `View() string`, cộng với vài setter. Không cần gì thêm.
- **Dữ liệu sạch, style áp lúc render:** các dòng luôn là text thuần; màu chỉ được thêm khi vẽ từng cell. Nhờ vậy việc tính độ rộng luôn đúng.
- **Đo độ rộng theo ô hiển thị:** `ansi.StringWidth`/`ansi.Truncate` hiểu cả escape code lẫn ký tự rộng (CJK chiếm 2 ô).
- **Chỉ render phần đang nhìn thấy:** với 2000 pod, chỉ khoảng 40 dòng được render mỗi frame.

### Thể hiện trong code

**Component table** (`internal/ui/table.go` → `tableModel`): API gần giống bubbles table (`SetColumns`, `SetRows`, `SetCursor`, `Cursor`, `SetWidth`, `SetHeight`, `Columns`, `Update`, `View`), nên `resourcesView` gần như chỉ phải đổi kiểu dữ liệu.

- `SetRows(rows [][]string, styles []lipgloss.Style)`: style là *song song* với rows, mỗi dòng một style foreground. Dữ liệu và cách trình bày tách riêng.
- `SetCursor` giữ con trỏ trong vùng nhìn thấy bằng `offset` (dòng đầu tiên đang hiển thị):

  ```go
  t.cursor = clamp(i, 0, len(t.rows)-1)
  switch {
  case t.cursor < t.offset:             t.offset = t.cursor              // cuộn lên
  case t.cursor >= t.offset+body:       t.offset = t.cursor - body + 1   // cuộn xuống
  }
  t.offset = clamp(t.offset, 0, max(len(t.rows)-body, 0))                // không để trống ở cuối khi dòng bị xóa
  ```

- `View` chỉ vẽ các dòng từ `offset` tới `offset+bodyHeight`. Mỗi cell được vẽ là `style.Render(fitCell(value, width))`, trong đó style là `tableSelectedStyle` (nền accent, chữ trắng đậm) nếu là dòng đang chọn, hoặc style màu của dòng đó. Vì **mỗi cell tự mang style đầy đủ** (gồm cả padding), nền của dòng đang chọn phủ liền mạch, không bị mã reset của cell trước cắt ngang.
- `fitCell`: `ansi.Truncate(s, width, "…")` rồi pad bằng dấu cách cho đủ `width - ansi.StringWidth(s)`, nên mọi dòng có cùng độ rộng hiển thị.
- Keymap của table chỉ gồm các phím di chuyển (`↑↓ jk`, `pgup/pgdown/b/space`, `ctrl+u/ctrl+d`, `g/G/home/end`). Nó cố ý **không** dùng `d`, `u`, `f` như bubbles table, nên không giành phím của view.

**Phân loại màu** (`internal/ui/colors.go`): đây là logic thuần, không có I/O.
- `statusHealth(status)`: so khớp từ khóa theo thứ tự **lỗi → đang chờ → đã xong**. Thứ tự quan trọng: `"NotReady"` chứa `"Ready"`, và `"Init:Error"` bắt đầu bằng `"Init:"`; nếu kiểm tra sai thứ tự thì một node hỏng sẽ có màu như node khỏe.
- `rowHealth(rt, resource)`: quyết định theo từng loại resource, và **chỉ dùng dữ liệu dòng đã có**, không gọi thêm API:
  - Pods: dùng `Pod.Status`; nếu Running mà `allReady(Pod.Ready)` là false (readiness probe đang fail) thì vàng.
  - Deployments: cell READY `a/b` với `a < b` thì vàng.
  - Nodes: cột STATUS.
  - Events: `Warning` thì vàng.
  - Services: không bao giờ tô màu.
- `healthStyles` dùng `lipgloss.AdaptiveColor`, nên màu vàng khác nhau trên nền sáng và nền tối.

**Nối vào bảng** (`resourcesView.setRows`): trong vòng lặp tạo rows có thêm `styles[i] = healthStyles[rowHealth(v.q.rt, e.res)]`. Màu tự cập nhật theo informer, vì mỗi event đều dựng lại rows.

### Test liên quan
- `internal/ui/table_test.go`:
  - `TestTableCursorScrolls`: offset khi cuộn, clamp, co lại khi dòng bị xóa, bảng rỗng;
  - `TestTableKeys`: `j`, `G`, `pgup`, `g`; **`d` không di chuyển con trỏ**;
  - `TestTableView`: header, chỉ vẽ các dòng nhìn thấy, cắt có `…`, mọi dòng cùng độ rộng (sau `ansi.Strip`);
  - `TestFitCell`: ký tự rộng `日本語`.
- `internal/ui/colors_test.go`: `TestStatusHealth` (bảng các trạng thái, đặc biệt `NotReady` và `Init:Error`), `TestRowHealth` (theo từng loại resource), `TestAllReady`.
- Màu không được kiểm tra trực tiếp trong unit test: khi không có TTY, lipgloss không xuất escape code. Thay vào đó, test kiểm tra *mức* (`health`), còn màu thật được kiểm tra thủ công qua `tmux capture-pane -e`.

### Bẫy
| Bẫy | Cách xử lý |
|---|---|
| bubbles table đếm escape code ANSI như ký tự | Tự viết `tableModel`, đo bằng `ansi.StringWidth` |
| Mã reset màu của cell xóa mất nền của dòng đang chọn | Áp style cho từng cell thay vì bọc cả dòng |
| `NotReady` khớp với `Ready`; `Init:Error` khớp với `Init:` | Kiểm tra lỗi trước |
| Test không thấy màu vì không có TTY | Test mức `health`; kiểm tra màu thật bằng `tmux capture-pane -e` |
| Ký tự rộng (CJK) làm lệch cột | `ansi.Truncate`/`StringWidth` tính theo ô hiển thị |

## 18. Phase 2.7: Nhớ namespace của mỗi context

### Mục tiêu
Lần sau mở kboba, hoặc khi chuyển sang lại một context, app tự quay về namespace đã dùng lần trước, kể cả "all namespaces". Dữ liệu được lưu vào **file riêng của kboba** (`$XDG_CONFIG_HOME/kboba/state.yaml`, thường là `~/.config/kboba/state.yaml`), tuyệt đối không ghi vào kubeconfig.

### Học được gì
- **Interface đặt ở phía dùng (consumer-side interface):** UI khai báo đúng 3 method nó cần (`NamespaceMemory`). Package `state` không biết UI tồn tại, còn UI không biết state được lưu ra sao. Test dùng một fake 20 dòng.
- **Tách phần nhanh và phần chậm:** cập nhật trong bộ nhớ thì làm ngay trong `Update`; ghi đĩa thì làm trong `tea.Cmd`.
- **Thứ tự hoàn thành của Cmd không xác định:** hai lần đổi namespace nhanh tạo ra hai Cmd lưu file chạy song song. Nếu mỗi Cmd mang theo giá trị *của nó* thì Cmd cũ có thể ghi đè giá trị mới. Giải pháp: Cmd chỉ "ghi trạng thái hiện tại", nên Cmd nào chạy sau cũng ghi giá trị mới nhất.
- **Ghi file atomic:** ghi ra file tạm rồi `rename` đè lên file cũ. `rename` là atomic trên cùng một filesystem, nên không bao giờ còn lại một file ghi dở.
- **"Không có" khác với "rỗng":** `""` nghĩa là all namespaces; key không tồn tại nghĩa là chưa nhớ gì. Map trong Go phân biệt được hai trường hợp này bằng `ns, ok := m[k]`.
- **Lỗi không chặn khởi động:** file state hỏng không được làm kboba không mở được.

### Thể hiện trong code

**Package `internal/state`** (`state.go`):
- `Open(path)`:
  - file không tồn tại thì trả về store rỗng, không lỗi;
  - file hỏng thì vẫn trả về **store dùng được** *kèm* lỗi, để lần `Save` sau sửa lại file.
- `LastNamespace` / `SetLastNamespace`: chỉ thao tác trên bộ nhớ, có `sync.Mutex` bảo vệ.
- `Save()`: dùng `yaml.Marshal` → `MkdirAll(dir, 0o700)` → `os.CreateTemp(dir, ".state-*.yaml")` → write → close → `os.Rename`. `defer os.Remove(tmp)` dọn file tạm nếu có bước nào lỗi; sau khi rename thành công thì lệnh remove này không còn tác dụng gì. Mutex được giữ suốt quá trình ghi, nên các lần `Save` không chồng lên nhau.
- `DefaultPath()` dùng `os.UserConfigDir()`, nên tôn trọng `$XDG_CONFIG_HOME`.

**Interface ở phía UI** (`internal/ui/root.go`):

```go
type NamespaceMemory interface {
    LastNamespace(context string) (namespace string, ok bool)
    SetLastNamespace(context, namespace string) // in memory, cheap
    Save() error                                // slow (disk); run in a tea.Cmd
}
```

`Options.Memory` có thể là `nil`, nghĩa là không nhớ gì; các test cũ không cần sửa.

**Thứ tự ưu tiên namespace** (`handleClientReady`): `--namespace` (chỉ khi khởi động) > namespace đã nhớ > namespace của context > `"default"`.

**Lưu** (`rememberNamespace`), được gọi sau khi kết nối và sau mỗi `switchNamespace`:

```go
if ns, ok := mem.LastNamespace(m.context); ok && ns == m.namespace {
    return nil                                   // không đổi → không đụng đĩa
}
mem.SetLastNamespace(m.context, m.namespace)     // ngay, trong Update
return func() tea.Msg {                          // ghi đĩa trong goroutine của Cmd
    if err := mem.Save(); err != nil {
        return statusMsg{text: "save state: " + err.Error(), isErr: true}
    }
    return nil
}
```

**Cảnh báo khi khởi động** (`Options.StartupWarning`, `statusSticky`): `main.go` truyền lỗi của `state.Open` vào. Nhưng chỉ một giây sau, thông báo info "watching 3 pods" sẽ ghi đè lên, và test đã phát hiện ra đúng lỗi này. Giải pháp: cảnh báo có cờ `statusSticky`. Khi cờ bật, `statusMsg` loại info bị bỏ qua (lỗi mới vẫn ghi đè được). Cờ được xóa ở phím bấm đầu tiên (`handleKey`), tức là khi người dùng đã có cơ hội đọc.

**`main.go`**: nếu `DefaultPath` lỗi (không xác định được thư mục config) thì chạy không có memory; nếu `Open` lỗi thì vẫn dùng store và hiện cảnh báo.

### Test liên quan
- `internal/state/state_test.go`:
  - `TestRoundTrip`: thư mục được tạo khi lưu; `""` được nhớ và khác với "không có"; không còn file tạm;
  - `TestCorruptFileStillUsable`: file hỏng vẫn cho store dùng được, và `Save` sửa lại file;
  - `TestConcurrentUse`: 20 goroutine cùng `Set` + `Save`.
- `internal/ui/root_test.go`:
  - `TestRememberedNamespace`: dùng namespace đã nhớ, không lưu khi không đổi, `:ns` thì lưu, context khác nhớ all namespaces;
  - `TestNamespaceFlagBeatsMemory`;
  - `TestStartupWarningShown`: cảnh báo không bị "watching N pods" đè, và nhường chỗ sau phím bấm đầu tiên.
- Thủ công: `XDG_CONFIG_HOME=<tmp> kboba` → `:ns all` → thoát → chạy lại sẽ mở ở all namespaces; ghi file hỏng thì vẫn mở được và thấy cảnh báo.

### Bẫy
| Bẫy | Cách xử lý |
|---|---|
| Hai Cmd lưu file hoàn thành sai thứ tự làm mất giá trị mới | Cập nhật bộ nhớ trong `Update`; Cmd chỉ ghi trạng thái hiện tại |
| Ghi dở file khi crash | File tạm + `rename` |
| `""` (all namespaces) bị coi là "chưa nhớ" | Dùng `ns, ok := map[key]` |
| File state hỏng làm app không mở được | `Open` trả về store dùng được kèm lỗi |
| Cảnh báo khởi động bị thông báo info đè ngay lập tức | `statusSticky` cho tới phím bấm đầu tiên |
| Ghi đĩa mỗi lần khởi động dù không có gì đổi | So sánh trước, không đổi thì không lưu |

## 19. Phase 2.8: Cột CPU/MEM từ metrics-server

### Mục tiêu
Bảng pods và nodes có thêm cột CPU (`250m`) và MEM (`128Mi`) giống `kubectl top`, tự cập nhật. Nếu cluster không có metrics-server thì hai cột hiện `-` và tiêu đề có `(no metrics)`; app không bị ảnh hưởng gì khác.

### Học được gì
- **Poll và watch:** metrics API (`metrics.k8s.io`) chỉ hỗ trợ `get`/`list`, không có `watch`. Đây là chỗ duy nhất kboba phải poll. Hãy so sánh với informer ở phase (c): poll thì có độ trễ tối đa bằng chu kỳ poll và luôn tốn request kể cả khi không có gì thay đổi; watch thì nhận thay đổi gần như tức thì và gần như không tốn gì khi cluster yên tĩnh.
- **Chọn chu kỳ poll có lý do:** 15 giây, khớp với chu kỳ thu thập mặc định của metrics-server. Poll nhanh hơn chỉ nhận lại đúng số liệu cũ.
- **Vòng poll trong Elm:** `fetch → loadedMsg → tea.Tick → tickMsg → fetch …`. Không có goroutine hay `time.Ticker` nào phải tự quản lý; generation counter đã có sẵn là đủ để dừng vòng lặp cũ.
- **Clientset thứ hai:** `k8s.io/metrics` có clientset riêng, nhưng dùng chung `rest.Config`, nên transport chỉ cho GET vẫn áp dụng.
- **`resource.Quantity`:** `Cpu().MilliValue()`, `Memory().Value()` chuyển các giá trị như `"250m"`, `"128Mi"` thành số.
- **Báo lỗi một lần:** một lỗi lặp lại mỗi 15 giây sẽ "spam" status bar và đè các thông báo khác.

### Thể hiện trong code

**k8s** (`internal/k8s/metrics.go` → `ListMetrics(ctx, rt, namespace, labelSelector)`):
- Pods: `MetricsV1beta1().PodMetricses(ns).List`, cộng usage của các container trong pod.
- Nodes: `NodeMetricses().List`.
- Kết quả là `map[key]Usage`, với key giống hệt `Resource.Key()` (`ns/name`, hoặc `/name` với node), nên UI ghép số liệu vào dòng chỉ bằng một lần tra map.
- Hỗ trợ cả label selector, nên khi drill-down vào pods của một deployment thì cũng chỉ lấy metrics của các pod đó.
- Type không có metrics trả về `ErrNoMetrics`; lỗi API được bọc kèm gợi ý `"is metrics-server installed?"`.
- `ResourceType.Metrics` (exported) = `true` cho Pods và Nodes; UI dựa vào cờ này để thêm cột.
- `NewClient` tạo `metricsclient.NewForConfig(cfg)` từ **cùng** `cfg` đã gắn `readOnlyTransport`.

**Vòng poll** (`internal/ui/resources.go`):

```go
// start(): với type có metrics, chạy song song watch và lần poll đầu
return tea.Batch(watch, fetchMetrics(c, q, gen))

case metricsLoadedMsg:
    if msg.gen != v.gen { return v, nil }               // query cũ → vòng lặp dừng ở đây
    next := tea.Tick(metricsInterval, func(time.Time) tea.Msg { return metricsTickMsg{gen: msg.gen} })
    ... lưu số liệu hoặc ghi nhận lỗi ...
    return v, next

case metricsTickMsg:
    if msg.gen != v.gen { return v, nil }
    return v, fetchMetrics(v.client, v.q, v.gen)
```

- Mỗi query chỉ có **một** chuỗi tick. Khi `start()` tăng `gen`, tick và kết quả của chuỗi cũ bị bỏ qua, và vì không ai lên lịch tick tiếp, chuỗi cũ tự dừng.
- `resourcesView.client` được giữ lại để lần tick sau gọi tiếp.
- Lỗi: xóa số liệu cũ (để không hiển thị số liệu sai mà trông như thật); chỉ `reportErr` khi chuyển từ OK sang lỗi (`metricsFailed`); vẫn poll tiếp, vì metrics-server có thể được cài sau đó.
- Tiêu đề có `(no metrics)` khi `metricsFailed`. Status bar có thể bị thông báo khác đè (smoke test đã cho thấy "watching 6 pods" đè lỗi metrics ngay lập tức), còn tiêu đề thì luôn hiện.

**Cột và format:**
- `columns()` thêm `CPU` và `MEM` trước `AGE` khi `rt.Metrics`.
- `usageCells(key)` trả về `"-"` khi chưa có số liệu.
- `formatCPU` luôn dùng millicore, `formatMemory` luôn dùng Mi. Dùng **cùng một đơn vị** để sắp xếp đúng: `naturalLess` so sánh phần số của `"1500m"` và `"250m"`, còn nếu trộn đơn vị (`1Gi` với `900Mi`) thì sẽ sai.

**kind:**
- `make kind-up` cài metrics-server (`METRICS_SERVER_VERSION`) và patch thêm `--kubelet-insecure-tls`, vì kubelet trong kind dùng chứng chỉ tự ký. Chỉ dùng cho môi trường local.
- `rbac.yaml` cấp `metrics.k8s.io` `pods`, `nodes` với verb `get`, `list`; role `kboba-limited` chỉ có `pods`, nên với context đó `:nodes` sẽ hiện `(no metrics)`.

### Test liên quan
- `internal/k8s/metrics_test.go`:
  - `TestListMetricsPods`: cộng container, lọc namespace, chỉ có action `list` trên `metrics.k8s.io`;
  - `TestListMetricsNodes`;
  - `TestListMetricsUnsupportedType`.
- `readonly_test.go`: `ListMetrics` có trong allowlist, được gọi cho mọi type, và các action của metrics fake chỉ là get/list/watch.
- `internal/ui/format_test.go` → `TestFormatUsage`.
- `internal/ui/root_test.go`:
  - `TestMetricsColumns`: cột và giá trị, `-` khi thiếu, services không có cột;
  - `TestMetricsErrorsReportedOnce`: lỗi chỉ báo một lần, số liệu cũ bị xóa, có `(no metrics)`;
  - `TestStaleMetricsTickStopsPolling`: tick và kết quả của query cũ không kích hoạt poll mới và không được áp dụng.

### Bẫy
| Bẫy | Cách xử lý |
|---|---|
| Fake metrics clientset: `NewSimpleClientset(objs...)` đoán resource là `podmetricses`, trong khi API dùng `pods`, nên list ra rỗng | Trong test, nạp bằng `Tracker().Create(gvr "pods"/"nodes", obj, ns)` |
| Nhiều chuỗi tick chạy song song sau khi đổi query | Generation guard: tick cũ không lên lịch tick mới |
| Lỗi bị báo mỗi 15 giây | `metricsFailed`, chỉ báo khi chuyển trạng thái |
| Lỗi metrics bị thông báo khác đè, người dùng không biết vì sao cột trống | `(no metrics)` trên tiêu đề |
| Trộn đơn vị (`Gi`/`Mi`) làm sắp xếp sai | Luôn dùng `m` và `Mi` |
| kind không có metrics-server, kubelet dùng chứng chỉ tự ký | Makefile cài metrics-server + `--kubelet-insecure-tls` (chỉ local) |

## 20. Phase 2.9: Mọi resource, kể cả CRD, qua discovery và dynamic client

### Mục tiêu
`:<bất kỳ loại nào>` đều mở được bảng live: `:cm`, `:ingresses`, `:statefulsets`, `:certificates.cert-manager.io`, `:wd`... Tên có thể là plural, singular, short name, kind hoặc `plural.group`. CRD hiện các cột từ `additionalPrinterColumns` giống `kubectl get`. `y` (YAML) và `/`, `s` (filter, sort) hoạt động với mọi loại.

### Học được gì
- **Discovery API:** server tự mô tả mình có những resource gì, ở group/version nào, có namespaced không, hỗ trợ verb nào, và short name là gì. `kubectl` cũng dựa vào nó để hiểu `kubectl get wd`.
- **Dynamic client và `unstructured`:** làm việc với object mà không cần Go type. Object chỉ là `map[string]any`, đọc bằng `unstructured.Nested*`.
- **Dynamic informer:** `dynamicinformer` cho informer trên bất kỳ GVR nào. Phần còn lại (handler, stop, gen) **không cần đổi gì**, vì cả hai loại factory cùng có `Start`/`Shutdown` và cùng trả về `cache.SharedIndexInformer`.
- **JSONPath:** cách `kubectl` tính giá trị cho printer columns, ví dụ `.status.conditions[?(@.type=="Ready")].status`.
- **Đọc CRD bằng chính dynamic client:** CRD cũng là một resource (`customresourcedefinitions.apiextensions.k8s.io`), nên không cần thêm thư viện apiextensions.
- **Tính an toàn khi chạy song song của thư viện:** `*jsonpath.JSONPath` giữ trạng thái trong lúc `Execute`, nên không dùng chung một instance cho nhiều goroutine được.
- **Read-only không có nghĩa là hiện mọi thứ:** đọc Secret là thao tác `get` hợp lệ, nhưng TUI thì dễ bị chia sẻ màn hình.

### Thể hiện trong code

**Tìm resource** (`internal/k8s/discovery.go` → `ResolveResourceType`):
1. `LookupResourceType(name)`: nếu là view có sẵn (pods, deploy, ...) thì dùng luôn.
2. `discovery.ServerPreferredResources(c.clientset.Discovery())`: lấy mọi resource, mỗi group ở version ưu tiên. Hàm này có thể trả **kết quả một phần kèm lỗi** (ví dụ khi một aggregated API như metrics đang down); kboba chỉ báo lỗi khi không có kết quả nào.
3. `findResource`: bỏ subresource (`pods/log`) và resource không có cả `list` lẫn `watch` (vì bảng của kboba chạy bằng informer); so khớp với `Name`, `SingularName`, `Kind` (chữ thường), `ShortNames`, và `name.group`.
4. Nếu GVR trùng với một view có sẵn (ví dụ `deployments.apps`) thì trả về view đó, vì view có sẵn có cột đẹp hơn.
5. Tạo một `ResourceType` với `dynamic: true`, `get` dùng dynamic client, `convert` dùng `unstructuredResource(columns)`, rồi **cache theo GVR**. Hỏi lại cùng một loại sẽ trả về đúng con trỏ cũ, nên phép so sánh `v.q.rt != q.rt` vẫn có nghĩa.

**Printer columns** (`printerColumns`, `parsePrinterColumns`):
- GET `customresourcedefinitions/<plural>.<group>` qua dynamic client. Core group thì bỏ qua (không bao giờ là CRD). Lỗi bất kỳ (NotFound vì là resource built-in, hoặc Forbidden) đều chỉ có nghĩa là "không có cột phụ", không làm hỏng view.
- Lấy `spec.versions[name == version đang dùng].additionalPrinterColumns`; bỏ cột `priority > 0` (chỉ hiện với `-o wide`), bỏ cột `.metadata.creationTimestamp` (kboba đã có AGE), và bỏ JSONPath không parse được.
- `printerColumn.eval` parse JSONPath **mỗi lần gọi** (`AllowMissingKeys(true)`) rồi `Execute` trên `u.Object`. Parse lại tốn rất ít, còn dùng chung instance thì có data race khi hai informer (cũ đang dừng, mới đang chạy) cùng convert.

**Một hàm watch cho cả hai loại** (`internal/k8s/resources.go` → `newInformer`):

```go
type informerFactory interface {
    Start(stopCh <-chan struct{})
    Shutdown()
}

if rt.dynamic {
    f := dynamicinformer.NewFilteredDynamicSharedInformerFactory(c.dynamic, 0, namespace, tweak)
    return f, f.ForResource(rt.gvr).Informer(), nil
}
f := informers.NewSharedInformerFactoryWithOptions(c.clientset, 0, informers.WithNamespace(namespace), informers.WithTweakListOptions(tweak))
```

Interface nhỏ `informerFactory` này là thứ duy nhất phải thêm. Mọi thứ khác trong `WatchResources` (handler, tombstone, `SetWatchErrorHandler`, thứ tự dừng) đã có từ phase (c) và được dùng lại nguyên vẹn.

**`get` nhận `*client`** thay vì `kubernetes.Interface`, để getter của type động dùng được `c.dynamic`. `toYAML` không cần đổi: `unstructured` cũng là `runtime.Object`, và `meta.Accessor` dùng được với nó.

**Che Secret** (`internal/k8s/yaml.go` → `redactSecret`): với `v1/secrets`, các giá trị trong `data`/`stringData` được thay bằng `<redacted, N chars>`, và annotation `last-applied-configuration` (chứa giá trị gốc) bị xóa. Key và kích thước vẫn được giữ, để vẫn debug được kiểu "secret có key `password` không".

**UI** (`internal/ui/root.go` → `runCommand`): nếu `LookupResourceType` không thấy thì không báo lỗi ngay, mà chạy `resolveResourceType` trong một Cmd (vì discovery cần gọi mạng), hiện "looking up …", rồi `resourceTypeResolvedMsg` sẽ gọi `showResources(rt)` hoặc báo lỗi. Ngoài phần này, UI không phải sửa gì: bảng, sort, filter, YAML, màu đều dựa trên `ResourceType` và `Resource`, vốn đã generic từ phase 2.1. **Đây là phần thưởng của abstraction ở phase 2.1.**

**kind:** `hack/kind/crd.yaml` (CRD `widgets` có printer columns), `widgets.yaml` (2 instance). Makefile apply CRD, `kubectl wait --for condition=established` (CR phải đợi CRD sẵn sàng), rồi mới apply phần còn lại. ClusterRole read-only thêm CRDs, widgets, configmaps, nhưng **không** có secrets.

### Test liên quan
- `internal/k8s/discovery_test.go` (dùng `FakeDiscovery.Resources` và `dynamicfake.NewSimpleDynamicClientWithCustomListKinds`):
  - `TestResolveResourceType`: view có sẵn được ưu tiên (kể cả `deployments.apps`); `widget`/`wd`/`Widget`/`widgets.example.com` trả về **cùng con trỏ**; resource không watch được, subresource và tên lạ đều bị từ chối;
  - `TestPrinterColumns`: chỉ lấy cột của version đúng; bỏ cột wide, Age và JSONPath hỏng; JSONPath có filter `[?(@.type=="Ready")]`;
  - `TestDynamicTypeWithoutCRDHasNameOnly`;
  - `TestWatchDynamicResources`: dynamic informer lọc theo namespace và tính được cell;
  - `TestGetYAMLDynamicAndSecretRedaction`: YAML của CR; giá trị Secret không lộ, key và annotation khác vẫn còn.
- `readonly_test.go` → `TestDynamicClientOnlyReads`: action của discovery, dynamic list/watch/get đều là đọc.
- `internal/ui/root_test.go`: `TestDiscoveredResourceType` (`:wd` → watch, rows, tiêu đề, `y`), `TestUnknownResourceTypeAfterDiscovery`.
- Thủ công trên envtest: `:wd` có SIZE/COLOR, sort `1 < 10`; `:cm` hiện NAME/AGE; `:secrets` với user read-only bị forbidden, với admin thì YAML đã che giá trị.

### Bẫy
| Bẫy | Cách xử lý |
|---|---|
| `FakeDiscovery.ServerPreferredResources()` (method) luôn trả về `nil` | Dùng hàm package `discovery.ServerPreferredResources(dc)`, vốn dựng kết quả từ `ServerGroups` và `ServerResourcesForGroupVersion` (fake có hỗ trợ) |
| Discovery lỗi một phần khi có aggregated API down | Chỉ báo lỗi khi không có kết quả nào |
| `jsonpath.JSONPath` không an toàn khi chạy song song | Parse mỗi lần `eval` |
| Mỗi lần resolve tạo `*ResourceType` mới nên phép so sánh con trỏ mất nghĩa | Cache theo GVR trong client (có mutex, vì gọi từ Cmd) |
| `kubectl apply` CR cùng lúc với CRD thì lỗi "no matches for kind" | `kubectl wait --for condition=established` giữa hai bước |
| `:secrets` hiện giá trị bí mật trên màn hình | `redactSecret`; ClusterRole demo không cấp quyền đọc secrets |
| Printer column `Age` trùng với cột AGE của kboba | Bỏ cột `.metadata.creationTimestamp` |

---

## Tổng kết Phase 2

Nhìn lại cả chuỗi phase, có một mạch rõ ràng:

1. **Phase (c)** dựng *một* pattern đúng: informer → channel → Cmd chờ → re-subscribe; generation guard; dừng rồi mới đóng channel.
2. **Phase 2.1** tách phần thay đổi theo loại resource (cột, cách chuyển object thành row) ra khỏi phần chung. Từ đó trở đi, mỗi tính năng chỉ cần nhắm vào phần chung là áp dụng được cho *mọi* loại: YAML (2.2), drill-down (2.4), sort (2.5), màu (2.6), metrics (2.8).
3. **Phase 2.9** thêm *vô số* loại resource mà UI gần như không phải sửa gì. Đó là dấu hiệu abstraction đã đặt đúng chỗ.

Một số nguyên tắc xuyên suốt: không block trong `Update`; mọi lời gọi chậm đi qua Cmd; message mang theo "phiên" của nó (gen/id) để bỏ được message cũ; logic thuần tách khỏi view để test bằng bảng case; read-only được khóa ở ba lớp (interface, fake actions, transport).

---

## 21. Phase 10: Kiểm tra quyền trước bằng SelfSubjectAccessReview

### Mục tiêu
Trước khi watch, xem logs hay poll metrics, kboba hỏi API server "tôi có được phép không?" (giống `kubectl auth can-i`). Nếu câu trả lời là không:
- tiêu đề hiện `(forbidden)` thay vì `loading…` mãi mãi;
- status bar nói chính xác hành động bị cấm, ví dụ `forbidden: you cannot list nodes cluster-wide`;
- **không** khởi động informer, nên không còn cảnh informer retry và báo lỗi liên tục;
- metrics bị cấm thì dừng hẳn vòng poll.

Để làm được việc này phải **nới lỏng guard read-only** cho đúng một loại request.

### Học được gì
- **Authorization API:** `SelfSubjectAccessReview` là một resource "ảo". Ta `create` nó với câu hỏi (`verb`, `group`, `resource`, `subresource`, `namespace`); server đánh giá bằng chính danh tính của ta và trả lời trong `status.allowed`. **Không có gì được lưu lại.** Mọi user đã xác thực đều được gọi nó, nhờ ClusterRole mặc định `system:basic-user`.
- **Nới lỏng một ràng buộc an toàn mà không làm nó mất tác dụng:** ngoại lệ phải hẹp nhất có thể (một method, một path), được viết thành code rõ ràng, và được khóa bằng test cả chiều "được phép" lẫn chiều "vẫn bị chặn".
- **Fail open và fail closed:** đây chỉ là bước kiểm tra trước để báo lỗi rõ hơn. Nếu bản thân request kiểm tra lỗi (server cũ, mạng chập chờn) thì cứ thử request thật; RBAC trên server vẫn là chốt chặn cuối cùng.
- **Thứ tự quan trọng với trải nghiệm người dùng:** lỗi phụ (metrics) không được che lỗi chính (không được list object).

### Thể hiện trong code

**Transport** (`internal/k8s/client.go` → `readOnlyTransport.RoundTrip`):

```go
const accessReviewPath = "/apis/authorization.k8s.io/v1/selfsubjectaccessreviews"

switch {
case req.Method == http.MethodGet || req.Method == http.MethodHead:
case req.Method == http.MethodPost && strings.HasSuffix(req.URL.Path, accessReviewPath):
default:
    return nil, fmt.Errorf("kboba is read-only: refusing %s %s", ...)
}
```

- So khớp bằng **suffix** vì API server có thể nằm sau một path prefix (ví dụ proxy kiểu `/k8s/clusters/c-1/apis/...`). Không có cách khớp lỏng hơn: `subjectaccessreviews` (hỏi quyền cho user *khác*), `selfsubjectrulesreviews`, `PUT`/`DELETE` lên cùng path, hay path dài hơn đều bị từ chối, và từng trường hợp đều có test.

**API kiểm tra quyền** (`internal/k8s/access.go`):
- `Access{Verb, Group, Resource, Subresource, Namespace}` và `String()` cho ra câu dễ đọc: `list deployments.apps in namespace "team-a"`, `get pods/log ...`, `list nodes cluster-wide`.
- Các hàm tạo `Access`, để UI không phải biết GVR (vốn là field unexported):
  - `(*ResourceType).Access(verb, ns)`: tự bỏ namespace khi resource là cluster-scoped;
  - `LogsAccess(ns)`: `get pods/log`;
  - `MetricsAccess(rt, ns)`: cùng resource nhưng group `metrics.k8s.io`.
- `(*client).CanI`: `AuthorizationV1().SelfSubjectAccessReviews().Create(...)` → `Decision{Allowed, Reason}`.
- `CheckAccess(ctx, client, actions...)`: trả về `*ForbiddenError` cho hành động đầu tiên bị cấm; nếu `CanI` lỗi thì trả về `nil` (fail open). `ForbiddenError` là một kiểu riêng, nên UI dùng `errors.As` để phân biệt "bị cấm" với các lỗi khác.

**Dùng ở UI**, luôn bên trong Cmd (vì là lời gọi mạng):
- `resourcesView.start` → trước `WatchResources`: `CheckAccess(list, watch)`. Nếu bị cấm thì trả về `watchStartedMsg{err}`, và `v.failed` được đặt, nên tiêu đề hiện `(forbidden)` (hoặc `(error)` với lỗi khác) thay cho `loading…`. `start()` sẽ reset `failed` cho query mới.
- **Metrics chỉ bắt đầu sau khi watch chạy được** (trong nhánh thành công của `watchStartedMsg`). Trước đây watch và metrics chạy song song; khi cả hai bị cấm, lỗi metrics đến sau và đè mất lỗi chính. Smoke test trên envtest đã phát hiện ra chuyện này.
- `fetchMetrics(..., first)`: chỉ lần poll đầu tiên của một query mới kiểm tra quyền. Nếu bị cấm thì trả về `metricsLoadedMsg{final: true}`, và handler không lên lịch tick nào nữa. Các lần poll sau không tốn thêm một request SSAR mỗi 15 giây.
- `logsView.restart` → trước `StreamLogs`: `CheckAccess(LogsAccess(ns))`. Lần kiểm tra dùng một context có timeout riêng, được tạo từ context của stream; nhờ vậy rời view giữa chừng thì lần kiểm tra cũng bị hủy.

**Test read-only được viết lại cho rõ ngoại lệ** (`readonly_test.go`):

```go
var allowedCreates = map[string]bool{"selfsubjectaccessreviews": true}

func isRead(a k8stesting.Action) bool {
    switch a.GetVerb() {
    case "get", "list", "watch": return true
    case "create":               return allowedCreates[a.GetResource().Resource]
    }
    return false
}
```

Ngoại lệ chỉ nằm ở **một chỗ**, có tên, có comment giải thích. Ai thêm ngoại lệ thứ hai sẽ phải sửa đúng map này, và reviewer sẽ thấy ngay. `CanI` được thêm vào allowlist method kèm comment giải thích.

### Test liên quan
- `readonly_test.go`:
  - `TestReadOnlyTransportRejectsWrites`: POST SSAR được phép (kể cả khi có path prefix); `PUT`/`DELETE` SSAR, `subjectaccessreviews`, `selfsubjectrulesreviews`, path dài hơn, và POST pod đều bị từ chối;
  - `TestClientOnlyReads`: có ít nhất một `create selfsubjectaccessreviews`, và không có action nào khác ngoài đọc.
- `access_test.go`:
  - `TestCanI`: fake API server trả lời theo một danh sách quyền (dùng `PrependReactor`); nodes bỏ namespace; mọi action đều chỉ là `create selfsubjectaccessreviews`;
  - `TestAccessDescriptions`;
  - `TestCheckAccess`: cho phép, bị cấm (`errors.As` lấy được `ForbiddenError`), và fail open.
- `internal/ui/root_test.go`:
  - `TestForbiddenWatchIsNotStarted`: không có informer nào được tạo; tiêu đề `(forbidden)` không kèm `loading`; đổi namespace được phép thì marker biến mất; nodes bị cấm thì không poll metrics và status giữ đúng lỗi chính;
  - `TestForbiddenLogs`: không stream log khi bị cấm;
  - `TestForbiddenMetricsStopsPolling`: không gọi `ListMetrics`, và lỗi `final` không lên lịch tick.
- Thủ công trên envtest với user `limited-user`: `:nodes` hiện ngay `Nodes[0] (forbidden)` cùng `forbidden: you cannot list nodes cluster-wide`, khớp với `kubectl auth can-i list nodes` → `no`.

### Bẫy
| Bẫy | Cách xử lý |
|---|---|
| Ngoại lệ trong transport khớp quá rộng (ví dụ cho mọi POST tới `authorization.k8s.io`) | Đúng một method, đúng một path (dạng suffix); test cả các trường hợp "gần giống" |
| Server cũ hoặc lỗi mạng khiến bước kiểm tra lỗi, làm kboba không dùng được | Fail open: `CheckAccess` trả về `nil` khi `CanI` lỗi |
| Lỗi metrics che lỗi chính khi cả hai bị cấm | Chỉ poll metrics sau khi watch đã chạy |
| Kiểm tra quyền metrics mỗi 15 giây tốn request | Chỉ kiểm tra ở lần poll đầu tiên của query |
| Fake clientset mặc định trả `allowed: false` (nó chỉ lưu object) | `PrependReactor("create", "selfsubjectaccessreviews", ...)` để tự trả lời |
| Tiêu đề kẹt ở `loading…` khi watch không bao giờ bắt đầu | `v.failed` hiện `(forbidden)` / `(error)` |

