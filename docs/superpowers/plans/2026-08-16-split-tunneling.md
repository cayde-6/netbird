# Split Tunneling Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Клиент направляет в туннель только заданные домены и подсети, никогда не устанавливая маршрут по умолчанию.

**Architecture:** Маршрут `0.0.0.0/0`, присланный management-сервером, переписывается в domain-маршрут в `DefaultManager.UpdateRoutes` — после фильтрации селектором и до установки системных маршрутов. За счёт этого переиспользуются существующие подсистемы: резолвер доменов по таймеру, HA, refcounting маршрутов и AllowedIPs. Список задаётся флагом `--split-tunnel` и персистится в профиль.

**Tech Stack:** Go 1.25.5, gRPC/protobuf (`client/proto/daemon.proto`), testify/require, cobra (CLI), launchd (macOS).

**Spec:** `docs/superpowers/specs/2026-08-16-netbird-split-tunneling-design.md`

## Global Constraints

- База: тег `v0.77.0`, ветка `split-tunnel`. Апстрим-совместимость не требуется.
- Go 1.25.5 или новее (из `go.mod`).
- Целевая платформа проверки — macOS (darwin/arm64); код платформенно-нейтральный.
- Новые тесты не должны требовать root: файл `splittunnel_test.go` пишется **без** build-тега `privileged` (в отличие от соседнего `manager_test.go`).
- Тесты — table-driven с `github.com/stretchr/testify/require`, как в существующем `client/internal/routemanager/manager_test.go`.
- Номера свободных полей protobuf: `LoginRequest` — 41 и далее (занято по 40), `GetConfigResponse` — 29 и далее (занято по 28).
- Целевые сервисы для натурной проверки: `gitlab.example.com`, `jira.example.com`. Оба отвечают `403` без VPN и `302` через VPN.
- Домашний внешний IP (признак прямого выхода): `203.0.113.10`.

---

### Task 0: Окружение и базовая сборка

Убедиться, что неизменённый клиент собирается, до внесения любых правок. Без этого шага непонятно, чем вызвана поломка — патчем или окружением.

**Files:**
- Изменений в репозитории нет.

**Interfaces:**
- Consumes: ничего.
- Produces: рабочий тулчейн `go`, `protoc`; собранный baseline-бинарь в `/tmp/netbird-baseline`.

- [ ] **Step 1: Установить тулчейн**

```bash
brew install go protobuf coreutils
```

`protobuf` даёт `protoc`, `coreutils` — `realpath`, который требует `client/proto/generate.sh`. Плагины `protoc-gen-go` скрипт ставит сам.

- [ ] **Step 2: Проверить версии**

```bash
go version    # ожидается go1.25.5 или новее
protoc --version
realpath --version | head -1
```

- [ ] **Step 3: Собрать неизменённый клиент**

```bash
cd /path/to/netbird
export PATH="/opt/homebrew/bin:$(go env GOPATH)/bin:$PATH"
go build -o /tmp/netbird-baseline ./client
```

PATH обязателен: `go` и `protoc` живут в `/opt/homebrew/bin`, а плагины
`protoc-gen-*`, которые `generate.sh` ставит через `go install`, — в
`$(go env GOPATH)/bin`. Без второго пути регенерация proto падает с
`protoc-gen-go: program not found`.

Первая сборка тянет зависимости, это несколько минут.

- [ ] **Step 4: Проверить, что бинарь рабочий**

```bash
/tmp/netbird-baseline version
```

Ожидается: `development-<12 символов хэша коммита>`. Версия `0.77.0` инжектится
только `goreleaser` через `-ldflags -X .../version.version=`; обычный `go build`
её не проставляет, поэтому `development-...` — корректный результат, а не поломка.

- [ ] **Step 5: Проверить, что регенерация proto работает на неизменённом файле**

```bash
cd client/proto && ./generate.sh && cd ../..
git diff --stat client/proto/
```

Ожидается: пустой diff. Если файлы изменились — версия `protoc` расходится с той, которой генерировали апстрим; зафиксировать это, но не откатывать: важно лишь, чтобы шаг проходил без ошибок.

- [ ] **Step 6: Зафиксировать возможный дрейф генерации**

Если предыдущий шаг дал непустой diff:

```bash
git add client/proto/
git commit -m "[build] Regenerate proto with local protoc"
```

Если diff пустой — шаг пропустить.

---

### Task 1: Разбор конфигурации — ParseSpec

**Files:**
- Create: `client/internal/routemanager/splittunnel.go`
- Test: `client/internal/routemanager/splittunnel_test.go`

**Interfaces:**
- Consumes: `domain.FromStringList` из `github.com/netbirdio/netbird/shared/management/domain`.
- Produces:
  - `type Spec struct { Domains domain.List; Prefixes []netip.Prefix }`
  - `func (s Spec) IsEmpty() bool`
  - `func ParseSpec(entries []string) (Spec, error)`

- [ ] **Step 1: Написать падающий тест**

Создать `client/internal/routemanager/splittunnel_test.go`:

```go
package routemanager

import (
	"net/netip"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/netbirdio/netbird/shared/management/domain"
)

func TestParseSpec(t *testing.T) {
	tests := []struct {
		name         string
		entries      []string
		wantDomains  domain.List
		wantPrefixes []netip.Prefix
		wantErr      bool
	}{
		{
			name:        "domains only",
			entries:     []string{"gitlab.example.com", "jira.example.com"},
			wantDomains: domain.List{"gitlab.example.com", "jira.example.com"},
		},
		{
			name:         "prefix only",
			entries:      []string{"10.50.0.0/16"},
			wantPrefixes: []netip.Prefix{netip.MustParsePrefix("10.50.0.0/16")},
		},
		{
			name:         "bare ip becomes host prefix",
			entries:      []string{"192.0.2.10"},
			wantPrefixes: []netip.Prefix{netip.MustParsePrefix("192.0.2.10/32")},
		},
		{
			name:         "mixed entries",
			entries:      []string{"gitlab.example.com", "10.50.0.0/16"},
			wantDomains:  domain.List{"gitlab.example.com"},
			wantPrefixes: []netip.Prefix{netip.MustParsePrefix("10.50.0.0/16")},
		},
		{
			name:    "empty and blank entries are skipped",
			entries: []string{"", "   "},
		},
		{
			name:    "nil entries",
			entries: nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			spec, err := ParseSpec(tc.entries)
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.wantDomains, spec.Domains)
			require.Equal(t, tc.wantPrefixes, spec.Prefixes)
		})
	}
}

func TestSpecIsEmpty(t *testing.T) {
	require.True(t, Spec{}.IsEmpty())
	require.False(t, Spec{Domains: domain.List{"example.com"}}.IsEmpty())
	require.False(t, Spec{Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}}.IsEmpty())
}
```

- [ ] **Step 2: Запустить тест и убедиться, что он падает**

```bash
go test ./client/internal/routemanager/ -run 'TestParseSpec|TestSpecIsEmpty' -v
```

Ожидается: FAIL, компиляция не проходит — `undefined: ParseSpec`, `undefined: Spec`.

- [ ] **Step 3: Написать минимальную реализацию**

Создать `client/internal/routemanager/splittunnel.go`:

```go
package routemanager

import (
	"fmt"
	"net/netip"
	"strings"

	"github.com/netbirdio/netbird/shared/management/domain"
)

// Spec is the parsed split tunnel configuration: the domains and prefixes that
// should be routed through the tunnel while everything else goes out directly.
type Spec struct {
	Domains  domain.List
	Prefixes []netip.Prefix
}

// IsEmpty reports whether the spec routes nothing. An empty spec leaves routes
// untouched, which keeps the full tunnel behaviour.
func (s Spec) IsEmpty() bool {
	return len(s.Domains) == 0 && len(s.Prefixes) == 0
}

// ParseSpec splits configured entries into prefixes and domains. An entry that
// parses as a prefix or a bare address is a prefix, everything else is a domain.
func ParseSpec(entries []string) (Spec, error) {
	var spec Spec
	var domains []string

	for _, entry := range entries {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}

		if prefix, err := netip.ParsePrefix(entry); err == nil {
			spec.Prefixes = append(spec.Prefixes, prefix)
			continue
		}
		if addr, err := netip.ParseAddr(entry); err == nil {
			spec.Prefixes = append(spec.Prefixes, netip.PrefixFrom(addr, addr.BitLen()))
			continue
		}

		domains = append(domains, entry)
	}

	if len(domains) > 0 {
		list, err := domain.FromStringList(domains)
		if err != nil {
			return Spec{}, fmt.Errorf("parse split tunnel domains: %w", err)
		}
		spec.Domains = list
	}

	return spec, nil
}
```

- [ ] **Step 4: Запустить тест и убедиться, что он проходит**

```bash
go test ./client/internal/routemanager/ -run 'TestParseSpec|TestSpecIsEmpty' -v
```

Ожидается: PASS для всех подтестов.

- [ ] **Step 5: Коммит**

```bash
git add client/internal/routemanager/splittunnel.go client/internal/routemanager/splittunnel_test.go
git commit -m "[client] Add split tunnel spec parsing"
```

---

### Task 2: Трансформация маршрутов — Transform

**Files:**
- Modify: `client/internal/routemanager/splittunnel.go`
- Test: `client/internal/routemanager/splittunnel_test.go`

**Interfaces:**
- Consumes: `Spec`, `Spec.IsEmpty` из Task 1; `vars.Defaultv4`, `vars.Defaultv6` из `client/internal/routemanager/vars`; `route.HAMap`, `route.Route`, `route.DomainNetwork`, `route.IPv4Network`, `route.IPv6Network`, `(*route.Route).GetHAUniqueID`.
- Produces: `func Transform(routes route.HAMap, spec Spec) route.HAMap`

Ключевые требования, которые проверяют тесты:

- дефолтный v4-маршрут превращается в domain-маршрут: `Domains` заполнены, `Network` обнулён, `NetworkType == route.DomainNetwork`, `KeepRoute == true`;
- `Peer`, `NetID`, `ID` доменного маршрута сохраняются от исходного;
- ключи результирующей `HAMap` пересчитываются через `GetHAUniqueID()`, поскольку он зависит от `NetString()`, а тот у domain-маршрута возвращает домены;
- HA-группа из нескольких пиров переписывается целиком, `Peer` у каждого сохраняется;
- недефолтные маршруты проходят насквозь без изменений;
- дефолтный v6-маршрут (`::/0`) отбрасывается: домены уже покрыты переписанным v4-маршрутом, а гнать весь IPv6 в туннель нельзя;
- пустой `Spec` возвращает исходную карту без изменений;
- каждый префикс даёт отдельный маршрут с уникальными `ID`/`NetID`.

- [ ] **Step 1: Написать падающий тест**

Дописать в `client/internal/routemanager/splittunnel_test.go`:

```go
func haMap(routes ...*route.Route) route.HAMap {
	m := route.HAMap{}
	for _, r := range routes {
		m[r.GetHAUniqueID()] = append(m[r.GetHAUniqueID()], r)
	}
	return m
}

func defaultRoute(id, netID, peer string) *route.Route {
	return &route.Route{
		ID:          route.ID(id),
		NetID:       route.NetID(netID),
		Peer:        peer,
		Network:     vars.Defaultv4,
		NetworkType: route.IPv4Network,
	}
}

func TestTransformRewritesDefaultRoute(t *testing.T) {
	spec := Spec{Domains: domain.List{"gitlab.example.com"}}
	in := haMap(defaultRoute("route-1", "Auto (Load Balancer)", "peer-tr"))

	out := Transform(in, spec)

	require.Len(t, out, 1)
	var got []*route.Route
	for _, rs := range out {
		got = rs
	}
	require.Len(t, got, 1)

	r := got[0]
	require.Equal(t, domain.List{"gitlab.example.com"}, r.Domains)
	require.Equal(t, route.DomainNetwork, r.NetworkType)
	require.False(t, r.Network.IsValid(), "network must be cleared on a domain route")
	require.True(t, r.KeepRoute, "KeepRoute keeps CDN addresses from being dropped")
	require.Equal(t, "peer-tr", r.Peer)
	require.Equal(t, route.NetID("Auto (Load Balancer)"), r.NetID)
	require.Equal(t, route.ID("route-1"), r.ID)
	require.True(t, r.IsDynamic())
}

func TestTransformKeyedByRewrittenHAID(t *testing.T) {
	spec := Spec{Domains: domain.List{"gitlab.example.com"}}
	in := haMap(defaultRoute("route-1", "Auto", "peer-tr"))

	out := Transform(in, spec)

	for key, rs := range out {
		require.Equal(t, rs[0].GetHAUniqueID(), key, "map key must match the rewritten route")
	}
}

func TestTransformPreservesHAGroup(t *testing.T) {
	spec := Spec{Domains: domain.List{"gitlab.example.com"}}
	in := haMap(
		defaultRoute("route-1", "Auto", "peer-tr"),
		defaultRoute("route-2", "Auto", "peer-ned"),
	)

	out := Transform(in, spec)

	require.Len(t, out, 1, "both routes share one HA group")
	var peers []string
	for _, rs := range out {
		for _, r := range rs {
			peers = append(peers, r.Peer)
		}
	}
	require.ElementsMatch(t, []string{"peer-tr", "peer-ned"}, peers)
}

func TestTransformPassesThroughNonDefaultRoutes(t *testing.T) {
	spec := Spec{Domains: domain.List{"gitlab.example.com"}}
	internal := &route.Route{
		ID:          "route-lan",
		NetID:       "lan",
		Peer:        "peer-tr",
		Network:     netip.MustParsePrefix("10.0.0.0/22"),
		NetworkType: route.IPv4Network,
	}
	in := haMap(internal)

	out := Transform(in, spec)

	require.Len(t, out, 1)
	require.Equal(t, internal, out[internal.GetHAUniqueID()][0])
}

func TestTransformDropsDefaultV6Route(t *testing.T) {
	spec := Spec{Domains: domain.List{"gitlab.example.com"}}
	v6 := &route.Route{
		ID:          "route-v6",
		NetID:       "Auto",
		Peer:        "peer-tr",
		Network:     vars.Defaultv6,
		NetworkType: route.IPv6Network,
	}
	in := haMap(v6)

	out := Transform(in, spec)

	require.Empty(t, out, "IPv6 default must not be tunneled")
}

func TestTransformEmptySpecIsNoop(t *testing.T) {
	in := haMap(defaultRoute("route-1", "Auto", "peer-tr"))

	out := Transform(in, Spec{})

	require.Equal(t, in, out)
}

func TestTransformAddsPrefixRoutes(t *testing.T) {
	spec := Spec{
		Domains:  domain.List{"gitlab.example.com"},
		Prefixes: []netip.Prefix{netip.MustParsePrefix("10.50.0.0/16")},
	}
	in := haMap(defaultRoute("route-1", "Auto", "peer-tr"))

	out := Transform(in, spec)

	require.Len(t, out, 2, "one domain route plus one prefix route")

	var prefixRoute *route.Route
	for _, rs := range out {
		for _, r := range rs {
			if !r.IsDynamic() {
				prefixRoute = r
			}
		}
	}
	require.NotNil(t, prefixRoute)
	require.Equal(t, netip.MustParsePrefix("10.50.0.0/16"), prefixRoute.Network)
	require.Equal(t, route.IPv4Network, prefixRoute.NetworkType)
	require.Equal(t, "peer-tr", prefixRoute.Peer)
	require.NotEqual(t, route.ID("route-1"), prefixRoute.ID, "prefix route needs its own ID")
}

func TestTransformPrefixOnlySpec(t *testing.T) {
	spec := Spec{Prefixes: []netip.Prefix{netip.MustParsePrefix("10.50.0.0/16")}}
	in := haMap(defaultRoute("route-1", "Auto", "peer-tr"))

	out := Transform(in, spec)

	require.Len(t, out, 1)
	for _, rs := range out {
		require.False(t, rs[0].IsDynamic())
		require.Equal(t, netip.MustParsePrefix("10.50.0.0/16"), rs[0].Network)
	}
}
```

Дополнить блок импортов в начале файла:

```go
import (
	"net/netip"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/netbirdio/netbird/client/internal/routemanager/vars"
	"github.com/netbirdio/netbird/route"
	"github.com/netbirdio/netbird/shared/management/domain"
)
```

- [ ] **Step 2: Запустить тест и убедиться, что он падает**

```bash
go test ./client/internal/routemanager/ -run TestTransform -v
```

Ожидается: FAIL, `undefined: Transform`.

- [ ] **Step 3: Написать минимальную реализацию**

Дописать в `client/internal/routemanager/splittunnel.go`:

```go
// Transform rewrites the default route into a domain route carrying the split
// tunnel domains and adds a route per configured prefix. Non-default routes pass
// through unchanged, the IPv6 default is dropped so IPv6 stays direct.
//
// It must run after the route selector has filtered exit nodes: a rewritten
// route no longer looks like an exit node to the selector, so transforming
// earlier would apply every offered exit node at once.
func Transform(routes route.HAMap, spec Spec) route.HAMap {
	if spec.IsEmpty() {
		return routes
	}

	result := route.HAMap{}
	add := func(r *route.Route) {
		id := r.GetHAUniqueID()
		result[id] = append(result[id], r)
	}

	for _, group := range routes {
		for _, r := range group {
			switch r.Network {
			case vars.Defaultv6:
				// Domains are already covered by the rewritten v4 route and the
				// whole v6 space must not be tunneled.
				continue
			case vars.Defaultv4:
				for _, rewritten := range rewriteDefault(r, spec) {
					add(rewritten)
				}
			default:
				add(r)
			}
		}
	}

	return result
}

// rewriteDefault turns a default route into the concrete routes the spec asks
// for, keeping the peer so HA failover keeps working.
func rewriteDefault(r *route.Route, spec Spec) []*route.Route {
	var out []*route.Route

	if len(spec.Domains) > 0 {
		domainRoute := *r
		domainRoute.Network = netip.Prefix{}
		domainRoute.Domains = spec.Domains
		domainRoute.NetworkType = route.DomainNetwork
		// Keep resolved addresses instead of dropping them, so a CDN swapping
		// addresses cannot break access between resolver ticks.
		domainRoute.KeepRoute = true
		out = append(out, &domainRoute)
	}

	for i, prefix := range spec.Prefixes {
		prefixRoute := *r
		prefixRoute.Network = prefix
		prefixRoute.Domains = nil
		prefixRoute.NetworkType = route.IPv4Network
		if prefix.Addr().Is6() {
			prefixRoute.NetworkType = route.IPv6Network
		}
		prefixRoute.ID = route.ID(fmt.Sprintf("%s-st%d", r.ID, i))
		prefixRoute.NetID = route.NetID(fmt.Sprintf("%s-st%d", r.NetID, i))
		out = append(out, &prefixRoute)
	}

	return out
}
```

Добавить в импорты этого файла `"github.com/netbirdio/netbird/client/internal/routemanager/vars"` и `"github.com/netbirdio/netbird/route"`.

- [ ] **Step 4: Запустить тесты и убедиться, что они проходят**

```bash
go test ./client/internal/routemanager/ -run 'TestTransform|TestParseSpec|TestSpecIsEmpty' -v
```

Ожидается: PASS во всех подтестах.

- [ ] **Step 5: Проверить, что пакет компилируется целиком**

```bash
go build ./client
go vet ./client/internal/routemanager/
```

- [ ] **Step 6: Коммит**

```bash
git add client/internal/routemanager/splittunnel.go client/internal/routemanager/splittunnel_test.go
git commit -m "[client] Rewrite default route into split tunnel routes"
```

---

### Task 3: Хранение списка в конфиге профиля

**Files:**
- Modify: `client/proto/daemon.proto` (сообщение `LoginRequest`, свободные поля с 41)
- Modify: `client/internal/profilemanager/config.go` (`ConfigInput` ~строка 103, `Config` ~строка 145, применение ~строка 635)
- Modify: `client/server/server.go` (~строка 529, рядом с обработкой `CleanDNSLabels`)
- Test: `client/internal/profilemanager/config_splittunnel_test.go`

**Interfaces:**
- Consumes: ничего из предыдущих задач.
- Produces:
  - поле `ConfigInput.SplitTunnel []string` (nil — не менять, пустой непустой slice — очистить)
  - поле `Config.SplitTunnel []string` (персистится в JSON профиля)
  - поля protobuf `LoginRequest.split_tunnel` (41) и `LoginRequest.cleanSplitTunnel` (42)

- [ ] **Step 1: Добавить поля в proto**

В `client/proto/daemon.proto`, в конец сообщения `LoginRequest` (после поля с номером 40):

```proto
  repeated string split_tunnel = 41;

  // cleanSplitTunnel clears the split tunnel list.
  // Needed because generated code omits empty slices due to omitempty tags.
  bool cleanSplitTunnel = 42;
```

- [ ] **Step 2: Регенерировать protobuf**

```bash
cd client/proto && ./generate.sh && cd ../..
grep -n 'SplitTunnel' client/proto/daemon.pb.go | head -5
```

Ожидается: в выводе видны сгенерированные поля `SplitTunnel` и `CleanSplitTunnel`.

- [ ] **Step 3: Написать падающий тест на конфиг**

Создать `client/internal/profilemanager/config_splittunnel_test.go`:

```go
package profilemanager

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestConfigSplitTunnel(t *testing.T) {
	tests := []struct {
		name    string
		initial []string
		input   []string
		want    []string
	}{
		{
			name:  "sets the list",
			input: []string{"gitlab.example.com"},
			want:  []string{"gitlab.example.com"},
		},
		{
			name:    "nil input keeps the existing list",
			initial: []string{"gitlab.example.com"},
			input:   nil,
			want:    []string{"gitlab.example.com"},
		},
		{
			name:    "empty non-nil input clears the list",
			initial: []string{"gitlab.example.com"},
			input:   []string{},
			want:    []string{},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			config := &Config{SplitTunnel: tc.initial}
			input := ConfigInput{SplitTunnel: tc.input}

			applySplitTunnel(config, input)

			require.Equal(t, tc.want, config.SplitTunnel)
		})
	}
}
```

- [ ] **Step 4: Запустить тест и убедиться, что он падает**

```bash
go test ./client/internal/profilemanager/ -run TestConfigSplitTunnel -v
```

Ожидается: FAIL — `config.SplitTunnel undefined`, `applySplitTunnel undefined`.

- [ ] **Step 5: Добавить поля и функцию применения**

В `client/internal/profilemanager/config.go` в структуру `ConfigInput`, рядом с `DNSLabels domain.List`:

```go
	// SplitTunnel lists domains and prefixes routed through the tunnel.
	// nil leaves the stored list alone, an empty non-nil slice clears it.
	SplitTunnel []string
```

В структуру `Config`, рядом с `DNSLabels domain.List`:

```go
	// SplitTunnel lists domains and prefixes routed through the tunnel.
	// When empty the client keeps its default full-tunnel behaviour.
	SplitTunnel []string
```

В конец того же файла:

```go
// applySplitTunnel copies the split tunnel list from input into config and
// reports whether anything changed.
func applySplitTunnel(config *Config, input ConfigInput) bool {
	if input.SplitTunnel == nil || slices.Equal(config.SplitTunnel, input.SplitTunnel) {
		return false
	}

	log.Infof("updating split tunnel list [ %v ] (old value: [ %v ])",
		input.SplitTunnel, config.SplitTunnel)
	config.SplitTunnel = input.SplitTunnel

	return true
}
```

Пакет `slices` уже импортирован в этом файле (используется для `DNSLabels`).

- [ ] **Step 6: Вызвать applySplitTunnel из общего применения конфига**

В `client/internal/profilemanager/config.go` сразу после блока `if input.DNSLabels != nil && !slices.Equal(...)` (около строки 641) добавить:

```go
	if applySplitTunnel(config, input) {
		updated = true
	}
```

- [ ] **Step 7: Запустить тест и убедиться, что он проходит**

```bash
go test ./client/internal/profilemanager/ -run TestConfigSplitTunnel -v
```

Ожидается: PASS.

- [ ] **Step 8: Пробросить поле из gRPC-запроса в конфиг**

В `client/server/server.go` сразу после блока обработки `msg.CleanDNSLabels` (около строки 533):

```go
	if msg.CleanSplitTunnel {
		config.SplitTunnel = []string{}
	} else if msg.SplitTunnel != nil {
		config.SplitTunnel = msg.SplitTunnel
	}
```

- [ ] **Step 9: Проверить сборку**

```bash
go build ./client
```

Собираем именно `./client` (демон), а не `./client/...`: пакет `client/ui`
не компилируется без собранного фронтенда (`pattern all:frontend/dist: no
matching files found`) — это предсуществующее состояние репозитория, к задаче
отношения не имеет, и GUI мы не трогаем.

- [ ] **Step 10: Коммит**

```bash
git add client/proto/ client/internal/profilemanager/ client/server/server.go
git commit -m "[client] Persist split tunnel list in the profile config"
```

---

### Task 4: CLI-флаг --split-tunnel

**Files:**
- Modify: `client/cmd/system.go` (константы флагов и переменные)
- Modify: `client/cmd/up.go` (регистрация флага ~строка 80, заполнение запроса ~строки 411 и 632, `ConfigInput` ~строка 512)

**Interfaces:**
- Consumes: поля protobuf `LoginRequest.SplitTunnel` / `CleanSplitTunnel` и `ConfigInput.SplitTunnel` из Task 3.
- Produces: флаг `--split-tunnel` у команды `up`.

- [ ] **Step 1: Объявить константу и переменную флага**

В `client/cmd/system.go` в блок `const` добавить:

```go
	splitTunnelFlag = "split-tunnel"
```

В блок `var` добавить:

```go
	splitTunnel []string
```

- [ ] **Step 2: Зарегистрировать флаг**

В `client/cmd/up.go`, рядом с регистрацией `dnsLabelsFlag` (около строки 80):

```go
	upCmd.PersistentFlags().StringSliceVar(&splitTunnel, splitTunnelFlag, nil,
		`Route only the listed domains and prefixes through the tunnel, `+
			`sending all other traffic directly. `+
			`An empty string "" clears the list and restores full-tunnel routing. `+
			`E.g. --split-tunnel gitlab.example.com,10.50.0.0/16 `+
			`or --split-tunnel ""`,
	)
```

- [ ] **Step 3: Заполнить gRPC-запрос**

В `client/cmd/up.go` есть два места сборки запроса: около строки 411 (`req.DnsLabels = ...`) и около строки 632 (`DnsLabels: dnsLabels,`).

Рядом со строкой 411 добавить:

```go
	req.SplitTunnel = splitTunnel
	req.CleanSplitTunnel = splitTunnel != nil && len(splitTunnel) == 0
```

В литерал структуры около строки 632 добавить поля:

```go
		SplitTunnel:      splitTunnel,
		CleanSplitTunnel: splitTunnel != nil && len(splitTunnel) == 0,
```

- [ ] **Step 4: Передать значение в ConfigInput для foreground-режима**

В `client/cmd/up.go` в литерал `ConfigInput` (около строки 512, где `DNSLabels: dnsLabelsValidated,`) добавить:

```go
		SplitTunnel:         splitTunnel,
```

- [ ] **Step 5: Собрать и проверить, что флаг виден**

```bash
go build -o /tmp/netbird-st ./client
/tmp/netbird-st up --help | grep -A 5 'split-tunnel'
```

Ожидается: в справке присутствует `--split-tunnel strings` с описанием.

- [ ] **Step 6: Коммит**

```bash
git add client/cmd/
git commit -m "[client] Add --split-tunnel flag to up command"
```

---

### Task 5: Врезка в route manager

**Files:**
- Modify: `client/internal/routemanager/manager.go` (`ManagerConfig` ~строка 115, конструктор ~строка 140, `UpdateRoutes` ~строка 438)
- Modify: `client/internal/engine.go` (`EngineConfig` ~строка 147, создание `ManagerConfig` ~строка 597)
- Modify: `client/internal/connect.go` (`createEngineConfig` ~строка 624)
- Test: `client/internal/routemanager/splittunnel_test.go`

**Interfaces:**
- Consumes: `ParseSpec`, `Transform`, `Spec` из Task 1 и 2; `Config.SplitTunnel` из Task 3.
- Produces: поле `ManagerConfig.SplitTunnel []string`; поле `DefaultManager.splitTunnel Spec`.

- [ ] **Step 1: Написать падающий тест на нормализацию доменов**

`domain.FromString` использует `idna.ToASCII` в нестрогом профиле, поэтому почти
не отвергает входные строки, но приводит их к нижнему регистру. Тест закрепляет
именно это — нормализацию, а не валидацию.

Дописать в `client/internal/routemanager/splittunnel_test.go`:

```go
func TestParseSpecNormalizesDomainCase(t *testing.T) {
	spec, err := ParseSpec([]string{"GitLab.Example.Com"})

	require.NoError(t, err)
	require.Equal(t, domain.List{"gitlab.example.com"}, spec.Domains)
}
```

- [ ] **Step 2: Запустить тест и убедиться, что он проходит**

```bash
go test ./client/internal/routemanager/ -run TestParseSpecNormalizesDomainCase -v
```

Ожидается: PASS. Тест проверяет уже реализованный в Task 1 код, поэтому падать
не должен; если он падает — расходится поведение `domain.FromStringList`, и
дальше идти нельзя, пока расхождение не понято.

- [ ] **Step 3: Добавить поле в ManagerConfig**

В `client/internal/routemanager/manager.go` в структуру `ManagerConfig` после `DisableServerRoutes bool`:

```go
	SplitTunnel         []string
```

- [ ] **Step 4: Разобрать список в конструкторе**

В `client/internal/routemanager/manager.go` в структуру `DefaultManager` рядом с `disableClientRoutes bool`:

```go
	splitTunnel         Spec
```

В `NewManager`, перед созданием `dm := &DefaultManager{...}`:

```go
	splitTunnel, err := ParseSpec(config.SplitTunnel)
	if err != nil {
		log.Errorf("Invalid split tunnel configuration, falling back to full tunnel: %v", err)
		splitTunnel = Spec{}
	}
	if !splitTunnel.IsEmpty() {
		log.Infof("Split tunnel enabled: %d domain(s), %d prefix(es)",
			len(splitTunnel.Domains), len(splitTunnel.Prefixes))
	}
```

И в литерал `dm := &DefaultManager{...}` добавить:

```go
		splitTunnel:         splitTunnel,
```

- [ ] **Step 5: Врезать трансформацию в UpdateRoutes**

В `client/internal/routemanager/manager.go` в методе `UpdateRoutes` заменить строку

```go
		filteredClientRoutes := m.routeSelector.FilterSelectedExitNodes(clientRoutes)
```

на

```go
		filteredClientRoutes := m.routeSelector.FilterSelectedExitNodes(clientRoutes)

		// Rewrite the default route into split tunnel routes. This has to run
		// after exit node filtering: a rewritten route no longer looks like an
		// exit node, so transforming earlier would apply every offered exit
		// node at once.
		filteredClientRoutes = Transform(filteredClientRoutes, m.splitTunnel)
```

- [ ] **Step 6: Добавить поле в EngineConfig**

Список едет из профиля в route manager по цепочке
`profilemanager.Config` → `createEngineConfig` → `EngineConfig` → `ManagerConfig`.
Промежуточное звено нужно создать явно.

В `client/internal/engine.go` в структуру `EngineConfig` рядом с
`DisableClientRoutes bool` (около строки 147):

```go
	SplitTunnel []string
```

- [ ] **Step 7: Заполнить EngineConfig из конфига профиля**

В `client/internal/connect.go` в функции `createEngineConfig` рядом со строкой
`DisableClientRoutes: config.DisableClientRoutes,` (около строки 624):

```go
		SplitTunnel:         config.SplitTunnel,
```

- [ ] **Step 8: Пробросить конфиг из движка в менеджер**

В `client/internal/engine.go` рядом со строкой `DisableClientRoutes: e.config.DisableClientRoutes,` (около строки 597) добавить:

```go
		SplitTunnel:         e.config.SplitTunnel,
```

- [ ] **Step 9: Проверить сборку и тесты**

```bash
go build ./client
go vet ./client/internal/routemanager/
go test ./client/internal/routemanager/ -run 'TestTransform|TestParseSpec|TestSpecIsEmpty' -v
```

Ожидается: сборка проходит, все тесты PASS.

- [ ] **Step 10: Коммит**

```bash
git add client/internal/routemanager/ client/internal/engine.go client/internal/connect.go
git commit -m "[client] Apply split tunnel routes in the route manager"
```

---

### Task 6: Сборка, установка и натурная проверка

Финальная задача: собрать демон, поставить вместо штатного и проверить поведение на живой системе.

**Files:**
- Изменений в репозитории нет.

**Interfaces:**
- Consumes: всё, реализованное в задачах 1–5.
- Produces: установленный пропатченный демон и подтверждённое поведение.

- [ ] **Step 1: Собрать демон**

```bash
cd /path/to/netbird
go build -o /tmp/netbird-split ./client
/tmp/netbird-split version
```

- [ ] **Step 2: Сохранить оригинальный бинарь**

Выполнять через нативный диалог macOS, пароль обрабатывает система:

```bash
osascript -e 'do shell script "cp /Applications/NetBird.app/Contents/MacOS/netbird /Applications/NetBird.app/Contents/MacOS/netbird.orig" with administrator privileges'
```

Проверить, что копия появилась:

```bash
ls -l /Applications/NetBird.app/Contents/MacOS/
```

- [ ] **Step 3: Остановить сервис и установить свой бинарь**

```bash
osascript -e 'do shell script "launchctl unload /Library/LaunchDaemons/netbird.plist; cp /tmp/netbird-split /Applications/NetBird.app/Contents/MacOS/netbird; codesign -f -s - /Applications/NetBird.app/Contents/MacOS/netbird; launchctl load /Library/LaunchDaemons/netbird.plist" with administrator privileges'
```

- [ ] **Step 4: Убедиться, что демон поднялся**

```bash
sleep 5
netbird status | head -8
```

Ожидается: демон отвечает, версия вида `development-<хэш>` (собственная сборка
без goreleaser-ldflags). Главное — демон отвечает, а хэш соответствует
собранному коммиту.

- [ ] **Step 5: Включить split tunneling**

`Server.Up` возвращается рано, если демон уже подключён, поэтому `netbird up`
с новым флагом на живом соединении сохранит список в профиль, но не применит его.
Нужен полный цикл:

```bash
netbird down
netbird up --split-tunnel gitlab.example.com,jira.example.com
sleep 15
```

- [ ] **Step 6: Проверить, что общий трафик идёт напрямую**

```bash
curl -s --max-time 10 https://1.1.1.1/cdn-cgi/trace | grep '^ip='
```

Ожидается: `ip=203.0.113.10` — домашний адрес, а не адрес exit-ноды.

- [ ] **Step 7: Проверить, что рабочие сервисы идут через туннель**

```bash
curl -s -o /dev/null -w 'gitlab=%{http_code}\n' --max-time 15 https://gitlab.example.com/
curl -s -o /dev/null -w 'jira=%{http_code}\n'   --max-time 15 https://jira.example.com/
```

Ожидается: `gitlab=302` и `jira=302`.

- [ ] **Step 8: Проверить таблицу маршрутов**

```bash
netstat -rn -f inet | grep -E 'utun100|^default'
```

Ожидается: **нет** строк `0/1` и `128.0/1`; есть точечные маршруты на адреса сервисов и служебный `10.0.0.0/22`.

- [ ] **Step 8b: Проверить поведение IPv6**

`jira.example.com` живёт на CloudFront и имеет AAAA-записи. Резолвер
динамических маршрутов добавляет и v6-префиксы, а v6-адреса у интерфейса
туннеля нет и v6-blackhole при split tunneling не ставится.

```bash
dig +short AAAA jira.example.com
netstat -rn -f inet6 | grep utun100 || echo "нет v6-маршрутов в туннеле"
curl -s -o /dev/null -w 'jira-v4=%{http_code}\n' -4 --max-time 15 https://jira.example.com/
curl -s -o /dev/null -w 'jira-v6=%{http_code}\n' -6 --max-time 15 https://jira.example.com/
```

Ожидается: `jira-v4=302`. Если `jira-v6` виснет или падает, а обычный (без `-4`)
запрос из шага 7 при этом отдаёт 302 — happy eyeballs справляется, ок.
Если обычный запрос ломается, зафиксировать это как отдельный дефект: маршруты
для AAAA уходят в туннель без v6-связности.

- [ ] **Step 9: Проверить, что настройка переживает переподключение**

```bash
netbird down && netbird up
sleep 15
curl -s --max-time 10 https://1.1.1.1/cdn-cgi/trace | grep '^ip='
curl -s -o /dev/null -w 'gitlab=%{http_code}\n' --max-time 15 https://gitlab.example.com/
```

Ожидается: тот же результат, что и в шагах 6–7, **без повторной передачи флага**.

- [ ] **Step 10: Проверить возврат к полному туннелю**

```bash
netbird up --split-tunnel ""
sleep 15
curl -s --max-time 10 https://1.1.1.1/cdn-cgi/trace | grep '^ip='
```

Ожидается: адрес exit-ноды (не домашний) — поведение вернулось к штатному.

- [ ] **Step 11: Вернуть split tunneling и зафиксировать результат**

```bash
netbird up --split-tunnel gitlab.example.com,jira.example.com
sleep 15
netbird status | head -8
```

- [ ] **Step 12: Записать процедуру отката**

Создать `docs/superpowers/plans/rollback.md` с содержимым:

```markdown
# Откат к штатному клиенту NetBird

    osascript -e 'do shell script "launchctl unload /Library/LaunchDaemons/netbird.plist; cp /Applications/NetBird.app/Contents/MacOS/netbird.orig /Applications/NetBird.app/Contents/MacOS/netbird; launchctl load /Library/LaunchDaemons/netbird.plist" with administrator privileges'

После отката список split tunnel остаётся в конфиге профиля, но штатный
клиент его игнорирует и восстанавливает полный туннель.

Обновление NetBird затирает пропатченный бинарь тем же образом: после
обновления нужно повторить сборку и установку из Task 6 плана.
```

- [ ] **Step 13: Коммит**

```bash
git add docs/superpowers/plans/rollback.md
git commit -m "[docs] Add rollback procedure for the patched daemon"
```

---

## Проверка полноты

Соответствие плана разделам спеки:

| Раздел спеки | Задача |
|---|---|
| Механизм: подмена маршрута | Task 2 |
| Плавающие адреса за CDN (`KeepRoute`) | Task 2, Step 1 и 3 |
| Точка врезки после `FilterSelectedExitNodes` | Task 5, Step 5 |
| Новый код: `ParseSpec`, `Transform`, `Spec` | Task 1, Task 2 |
| Конфигурация и CLI | Task 3, Task 4 |
| Краевой случай: нет дефолтного маршрута | Task 2 (`TestTransformPassesThroughNonDefaultRoutes`) |
| Краевой случай: HA-группа | Task 2 (`TestTransformPreservesHAGroup`) |
| Краевой случай: пустой список | Task 2 (`TestTransformEmptySpecIsNoop`), Task 6 Step 10 |
| Краевой случай: IPv6 | Task 2 (`TestTransformDropsDefaultV6Route`) |
| Тестирование: юнит-тесты | Task 1, Task 2 |
| Тестирование: натурный сценарий | Task 6 |
| Сборка и установка | Task 0, Task 6 |
| Риски: откат и переустановка после обновления | Task 6, Step 12 |
