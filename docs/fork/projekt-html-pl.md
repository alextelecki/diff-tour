---
status: draft (po recenzji Fable 2026-10-23 — 10 punktów naniesionych)
data: 2026-10-23
galaz: html-pl
upstream: init-co-jp/diff-tour @ eeaf897 (v0.5.0+), MIT, PR-ów nie przyjmuje (close-prs.yml) — fork to jedyna droga
---

# Fork diff-tour: trasa jako HTML, po polsku, z pytaniami w przeglądarce

## Cel

`tour` pisze świetne trasy, ale ogląda się je tylko w terminalu i nie po polsku. Chcemy:

1. **plik HTML** z tą samą trasą — lepsza wizualizacja, diagramy w **Mermaid**;
2. **polski** jako język trasy i interfejsu;
3. **pytania w przeglądarce** — jak `?` w TUI: pytanie → odpowiedź dopisana do trasy;
4. **determinizm**: LLM pisze tylko JSON trasy (opis i odnośniki), a cały wygląd robi kod. Ta sama trasa →
   ten sam HTML, te same style. Kod na ekranie zawsze z gita, nigdy z odpowiedzi modelu (zasada upstreamu).

## Jak to wygląda

```
                 (bez zmian)                                   (nowe)
tour <zmiana> --lang pl ──► claude -p (tylko odczyt) ──► tour.json ──► web.Render ──► trasa.html
                             prompty + lekcje upstreamu     │            │  kod/diff/schemat z gita
                                                            │            │  flow → Mermaid flowchart
                                                            │            │  timeline → Mermaid sequenceDiagram
                                                            │            │  chroma → HTML z klasami
                                                            ▼
tour serve <zmiana|tour.json> ──► http://127.0.0.1:<port>/   (token w stronie, nagłówek X-Tour-Token)
                                    │  GET  /        → ta sama strona co --html
                                    │  POST /ask     → generate.Answer (istniejący) → nowe kroki
                                    │                  → walidacja + render.Check (istniejące)
                                    │                  → Tour.InsertAfter → zapis atomowy tour.json → fragment HTML kroków
                                    ▼
                                  przeglądarka wstawia kroki po bieżącym
```

## Zakres

### 1. Polski (`internal/i18n`, `internal/generate`)
- `i18n.PL Lang = "pl"`: w `Langs`, `Name()` („Polski”), `Parse`, `Detect` (`pl_PL…` z `LANG`).
- 101 komunikatów UI po polsku (mapa `messages`), `TestTranslatedMessages` pilnuje kompletu.
- `languageRules[PL]`: „Pisz cały tekst po polsku, krótkimi, prostymi zdaniami. Identyfikatory, nazwy plików,
  polecenia i przyjęte terminy techniczne zostaw w oryginale.”
- **Prompty i lekcje zostają po japońsku.** Język wyjścia ustala `languageRules` — tak samo robi upstream
  dla en/zh/es. Tłumaczenie promptów to duży koszt i konflikty przy każdej synchronizacji z upstreamem,
  a nic nie daje modelowi. (Do zmiany, jeśli trasy po polsku wyjdą słabsze niż po angielsku.)

### 2. Renderer HTML (`internal/web`, nowy pakiet)
- `web.Render(repo, tour, lang) ([]byte, error)` — jeden samodzielny plik HTML (CSS, JS i Mermaid wbudowane
  przez `go:embed`; strona nie robi żadnych żądań do sieci — obietnica upstreamu „tour makes no network
  requests” zostaje).
- Dane czyta to samo co TUI: `git.Repo.Show/Diff`, `schema.Load`, `tour.Validate`. Układ TUI (`render/*`,
  linie z SGR) **nie** jest używany — HTML ma własne szablony (`html/template`, escapowanie z urzędu).
- Bloki → HTML, każdy typ z `tour.Block`:

  | blok | HTML |
  |---|---|
  | `p` + `basis` | akapit z etykietą „Sprawdzone / Z kodu / Zdanie seniora” (kolor jak w TUI) |
  | `quote`, `list`, `pre`, `table`, `rows`, `snippet` | odpowiednie elementy; `snippet` z etykietą **Przykład** |
  | `code` | kod z gita przy `at`, numery linii, podświetlenie przez istniejące `highlight.Lines` — 5 stylów mapowanych na klasy CSS (ten sam wygląd co TUI, bez nowej palety), `marks` jako dymki przy linii, `show` zwija resztę |
  | `diff` | wybrane hunki z gita, kolorowanie +/− |
  | `schema` | tabele z migracji (`schema.Load`); `Schema` to mapa — klucze sortowane |
  | `files` | mapa plików: rola, notatka, +/−, pliki „mechanical” zwinięte |
  | `flow` | **Mermaid** `flowchart TD`: węzły `n0…nN`, krawędź `n(i-1) -->|Next| n(i)`, brak krawędzi przy `start`; `kind` → klasa (`new`, `changed`…) |
  | `timeline` | **Mermaid** `sequenceDiagram`: `participant` z `lanes`, `to` → strzałka, bez `to` → `Note over`, `mark` → symbol (✔ ✘ ★) |
  | `target` | odnośnik „gdzie dalej patrzeć” |
- Krok: tytuł, bloki; krok z `question` — pytanie i przycisk „Pokaż odpowiedź” (odpowiedź = następny krok,
  ukryty do kliknięcia — jak „pomyśl, potem →” w TUI).
- Nawigacja: spis rozdziałów z boku, **← →** po krokach (jak w TUI), pasek postępu, link do kroku w adresie
  (`#k7`). Bez frameworka JS — kilkadziesiąt linii własnego skryptu.
- Mermaid: przypięta wersja w nazwie pliku (`internal/web/assets/mermaid-<wersja>.min.js`, ~3 MB w każdym HTML —
  cena „jeden plik, bez sieci”). W kodzie i teście jawnie `securityLevel: 'strict'`, `startOnLoad: false`,
  `mermaid.run()` po załadowaniu i po wstawieniu kroków z `/ask`.
- **Escapowanie pod Mermaid** — jedyne miejsce, gdzie tekst modelu wchodzi w składnię, a nie w `html/template`:
  węzły `n0["…"]`, uczestnicy `participant p0 as "…"`, encje `#quot;` `#35;` `#59;` (średnik kończy instrukcję
  w `sequenceDiagram`), nowe linie → `<br/>`; inline markup (`**b**`, `` `c` ``, `text/text.go:164`) zrzucany
  do zwykłego tekstu. Test tabelowy z `"`, `;`, `#`, `[]`, `|`, `<script>`.
- **Deterministyczność:** bez dat, losowości i kolejności z map w wyjściu → test złoty (golden) pilnuje,
  że ta sama trasa daje bajt w bajt ten sam HTML.

### 3. CLI (`cmd/tour`)
- `tour <zmiana|tour.json> --html <plik>` — jak dziś (wygeneruj albo weź z cache), ale zamiast TUI zapisz
  HTML. Działa bez terminala: TTY to tylko strażnik CLI (`main.go:63-72`), `Generate` idzie przez pipe'y —
  omijamy strażnika jak `--dump` (`main.go:155`), wołamy `generate.Generate` + `SaveCached` (wzór
  `ui/app.go:176-184`), postęp na stderr. Pierwsze uruchomienie bez configu nadal wymaga `tour setup`.
- `tour serve <zmiana|tour.json> [--port N]` — serwer na `127.0.0.1` (nigdy `0.0.0.0`); drukuje adres i
  best-effort otwiera przeglądarkę (`open`/`xdg-open`). `--help` mówi, że `serve` dopisuje odpowiedzi do pliku trasy.
- `tour <zmiana> --lang pl` — istniejąca flaga, nowy język.

### 4. Pytania w przeglądarce (`internal/web/serve.go`)
- `POST /ask {after: <indeks kroku>, question}` → `generate.Answer` (`ask.go:24`, zależy tylko od `tour` — bez
  `ui`/bubbletea; workspace z eksportem gita, `claude -p` tylko do odczytu, walidacja + `render.Check`, do 2
  poprawek) → `Tour.InsertAfter` (`tour.go:334`, te same 5 linii co `ui/ask.go:93-100`) → **zapis atomowy**
  (plik tymczasowy + `rename`) → odpowiedź: HTML nowych kroków.
- Formularz pokazuje podpowiedzi kroku (`Step.Asks`, jak `ui/ask.go:57`); po wczytaniu trasy
  `DropRevealingAsks` jak w TUI.
- Dziś pytania do `tour <plik.json>` znikają po wyjściu (`Save` = nil, `main.go:168-169` — świadoma obietnica
  upstreamu); w `serve` zapisujemy do pliku, z którego trasa przyszła (cache albo podany `.json`) — to jawna
  zmiana tej obietnicy, opisana w `--help`.
- Odpowiedź trwa minuty: przycisk blokuje się, widać „Claude czyta kod…”, żądanie czeka (bez kolejki,
  jedno pytanie naraz przez `sync.Mutex.TryLock` — drugie dostaje 409).
- **Bezpieczeństwo:** tylko `127.0.0.1`; `Host` musi być `127.0.0.1:<port>` (DNS rebinding); losowy token
  wstawiony w stronę, wymagany w nagłówku `X-Tour-Token` każdego `POST /ask` z ciałem JSON (inna strona
  w przeglądarce nie odpali `claude` przez CSRF — nie ustawi nagłówka bez preflightu, a SOP nie da jej
  odczytać tokenu). Tekst modelu: HTML przez `html/template`, Mermaid przez escapowanie + `securityLevel:
  'strict'`. Model ma te same ograniczenia co w upstreamie.

## Poza zakresem
- Surowy blok `mermaid` pisany przez LLM — diagramy idą tylko z `flow`/`timeline` (deterministycznie,
  walidowane). Jeśli zabraknie typów diagramów, dodamy typ bloku z walidacją, nie wolny tekst.
- Zmiany w TUI, w promptach, w backendach innych niż Claude Code (dalej działają, nietestowane przez nas).
- Hosting tras, udostępnianie innym, eksport do PDF.
- Tłumaczenie lekcji i promptów na polski.
- Skill `/tour` w Claude Code — można dodać później jako cienką nakładkę na `tour --html`.

## Testy
| co | jak |
|---|---|
| i18n PL | `TestTranslatedMessages` (101 kluczy), `Parse("pl")`, `Detect` z `LANG=pl_PL.UTF-8` |
| Mermaid | testy tabelowe: flow (krawędzie, `start`, etykiety, znaki do escapowania), timeline (strzałka, notatka, mark) |
| HTML | złoty plik: mała trasa na repo gita zakładanym w teście (każdy typ bloku) → `testdata/*.html` bez wbudowanego JS Mermaid (w złotym tylko hash pliku); drugi render = te same bajty |
| escapowanie | tekst z `<script>` w JSON-ie trasy nie wychodzi jako znacznik |
| serwer | `httptest` z podstawionym `Answer`: brak tokenu → 403, zły `Host` → 403, równoległe pytanie → 409, odpowiedź zapisana atomowo do pliku i zwrócona jako HTML |
| Mermaid w stronie | `securityLevel: 'strict'` obecne w wyjściu (test na tekst strony) |
| całość | `go vet ./...`, `go test ./...`, `gofmt -l .` puste (jak CI upstreamu) |
| ręcznie | `tour 9da55906 --lang pl --html /tmp/t.html` na forest PR #468 i `tour serve` z jednym pytaniem |

## Plan
| # | slice | wynik |
|---|---|---|
| S1 | polski | `tour --lang pl` pisze trasę po polsku, TUI po polsku |
| S2 | renderer HTML + `--html` | plik HTML z Mermaid dla trasy #468 |
| S3 | `serve` + pytania | pytanie w przeglądarce dopisuje kroki do trasy |

Jeden PR na koniec (do własnego forka), recenzja osobnym agentem, pełne `go vet/test/gofmt` raz na końcu.

## Decyzje Alexa (2026-10-23)
1. Fork na GitHubie: `alextelecki/diff-tour`.
2. Binarka zostaje `tour` — zbudowana z forka zastępuje zainstalowaną z upstreamu (`~/.local/bin/tour`).
