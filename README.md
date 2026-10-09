# kboba

A tiny, **read-only**, k9s-like terminal UI for Kubernetes, built with
[Bubble Tea](https://github.com/charmbracelet/bubbletea) and
[client-go](https://github.com/kubernetes/client-go).

kboba is a learning project: the code favours being small and easy to read
over features.

## Features

- **Contexts**: list kubeconfig contexts and switch between them (in memory
  only, your kubeconfig is never written).
- **Namespaces**: pick a namespace, or "all namespaces".
- **Pods**: live table (NAME, READY, STATUS, RESTARTS, AGE, plus NAMESPACE
  in all-namespaces mode) driven by an informer, no polling.
- **Logs**: follow a container's logs, toggle auto-scroll, cycle containers.
- **Describe**: pod status, containers, conditions and related events.
- Filter by name with `/`, k9s-style `:` commands, `?` for help.
- Errors (connection refused, RBAC forbidden, deleted pods, unknown
  contexts) are shown in the status bar.

## Read-only by design

kboba only issues `get`, `list` and `watch` requests:

- The `k8s.Client` interface only has read methods, and a test
  (`TestClientInterfaceIsReadOnly`) fails if a method is added without being
  reviewed.
- `TestClientOnlyReads` runs every method against client-go's fake clientset
  and checks that every recorded action is a `get`, `list` or `watch`.
- As defence in depth, the HTTP transport refuses any method other than
  `GET`/`HEAD` (watches and log streaming are `GET` too).

## Install / run

Requires Go 1.24+ (client-go v0.33 needs it).

```sh
make build
./bin/kboba [--kubeconfig PATH] [--context NAME] [--namespace NAME]
```

Without flags kboba uses `$KUBECONFIG` or `~/.kube/config`, its
`current-context` and that context's namespace.

## Keys

| Where       | Key            | Action                              |
|-------------|----------------|-------------------------------------|
| everywhere  | `:`            | command bar                          |
|             | `?`            | toggle full help                     |
|             | `esc`          | back / clear filter                  |
|             | `q`, `ctrl+c`  | quit                                 |
| pods        | `enter`        | logs of the selected pod             |
|             | `d`            | describe the selected pod            |
|             | `/`            | filter by name                       |
| logs        | `f`            | toggle follow (auto-scroll)          |
|             | `c`            | next container                       |
|             | `↑↓ pgup pgdn` | scroll (scrolling up pauses follow)  |
| describe    | `r`            | refresh                              |

Commands: `:pods`, `:ctx [name]`, `:ns [name|all]`, `:q`.

## Try it with kind (read-only ServiceAccount)

Needs [kind](https://kind.sigs.k8s.io/), `kubectl` and Docker.

```sh
make run-kind     # create cluster, apply samples, run kboba read-only
make kind-down    # delete the cluster
```

`make kind-up` creates a kind cluster called `kboba` with its **own**
kubeconfig in `.kind/`, so your `~/.kube/config` is not touched and no other
cluster is targeted. It deploys to the `kboba-demo` namespace:

- `chatty`: logs a line every second
- `crashloop`: exits with an error, ends up in `CrashLoopBackOff`
- `multi`: two containers (`c` switches between them in the logs view)
- `bad-image`: `ImagePullBackOff`

`make kind-kubeconfig` writes `.kind/readonly.kubeconfig` with three
contexts:

| Context             | Permissions                                                        |
|---------------------|--------------------------------------------------------------------|
| `kboba-readonly`    | ClusterRole with get/list/watch on pods, pods/log, namespaces, events |
| `kboba-limited`     | Same verbs, only inside `kboba-demo` (try `:ns` and `:ns all`)     |
| `kboba-unreachable` | Points at a dead endpoint, to see connection errors                |

## Layout

```
cmd/kboba/          flags, wiring
internal/k8s/       client-go only: kubeconfig, informer, logs, describe
internal/ui/        Bubble Tea: root model + one sub-model per view
hack/kind/          sample workloads, RBAC, kubeconfig generator
```

`internal/k8s` never imports Bubble Tea; the UI talks to it through the
small `k8s.Client` interface, which the UI tests replace with a fake.

### How data flows

- **Every slow call is a `tea.Cmd`** that returns a message; `Update` never
  blocks.
- **Pods**: `WatchPods` starts a `SharedInformerFactory`. Its event handlers
  push `PodEvent`s into a channel. The UI wraps the channel in a `tea.Cmd`
  that waits for the next batch of events; handling that message re-issues
  the command. Changing context or namespace calls `Stop()` (closes the stop
  channel) before a new informer is created, and a generation counter drops
  any event still in flight from the old one.
- **Logs**: `StreamLogs` opens `GET pods/log?follow=true`, reads it with a
  `bufio.Scanner` in a goroutine and pushes lines into a channel, using the
  same wait/re-subscribe pattern. Leaving the view cancels the context,
  which closes the connection and ends the goroutine. Lines are kept in a
  5000-line ring buffer and the viewport is re-rendered once per batch.

## Development

```sh
make test   # unit tests
make lint   # go vet (+ golangci-lint if installed)
```
