# Contexto AI — Workspace `myTools`

> **Fichero vivo.** Este documento existe para que cualquier IA que trabaje aquí entienda el
> entorno y pueda continuar el desarrollo sin partir de cero. **Añade aquí toda instrucción,
> decisión o conocimiento importante** que me vayas dando. Cuando leas este fichero, trátalo
> como la fuente de verdad: actualízalo cuando aprendas algo nuevo relevante (sección 7).

- **Ruta:** `/home/jips/datos/datos-juan/proyectos/myTools/context-ai.md`
- **Creado:** 2026-11-17
- **Autor del código:** Juan Ignacio Pérez Sacristán (`computerphysicslab`,
  computerphysicslab@gmail.com)
- **Idioma del proyecto:** código y comentarios en inglés; documentación y este contexto en español.

---

## 1. Estructura del workspace `myTools`

La raíz **no es un repositorio Git**; cada subproyecto puede serlo. Ocupación total ≈ 3,4 GB.

| Carpeta | Tamaño | Descripción |
|---|---|---|
| `goLang-goCrawler/` | **2,2 GB** | Proyecto principal (ver §2). El 95% del peso son logs. **Repo Git aquí** (dentro de `goCrawler/`). |
| `AI/` | 1,2 GB | Proyectos/experimentos de IA (no analizados en detalle aún). |
| `DocSpider/` | 33 MB | Otro spider (no analizado aún). |
| `twitter/`, `youTube/`, `AutoScreenShot/`, `linkedin-video-download/` | < 2 MB | Utilidades pequeñas (no analizadas aún). |

> Si más adelante quieres que documente `AI/`, `DocSpider/` u otros, dímelo y amplío este fichero.

---

## 2. Proyecto principal: `goCrawler`

**Ruta:** `goLang-goCrawler/goCrawler/` · **Repo Git:** sí · **Último commit:** `1be79f6 fix` (2023-05-10).

### 2.1 Qué hace

Crawler temático en **Go 1.20** (módulo `goCrawler`; migrado desde 1.13 en 2026-11-17). No
rastrea la web indiscriminadamente: parte de URLs semilla (`bootstrapingLinks`), descarga páginas,
extrae enlaces y texto plano, y **solo conserva documentos que pasan filtros de idioma (inglés),
longitud, relevancia por palabras clave y unicidad**. Resultado: un **corpus de texto curado por
tema** + estadísticas de frecuencias y detección de n-gramas. Usa **SQLite local** como caché de
 URLs descargadas (Redis ya no es obligatorio, ver §2.9), **Viper** para YAML por perfil y
`whatlanggo` para detección de idioma.

README: *"goLang crawler restricted to only topic relevant/curated URLs. It includes token
frequency analysis and NLP nGram detection"*. Demo: https://youtu.be/7IDkNYcMXxU

### 2.2 Estructura de código

| Ruta | Función |
|---|---|
| `crawler.go` (1560 líneas) | **Punto de entrada** (`main`, líneas 1477-1543). Descarga, filtra, puntúa, escribe corpus y output. |
| `corpusfreqlib/` | Corpus de referencia del inglés (BNC `all.num` + `contemporaryEnglish.txt`). Expone `Freq(token)` para calcular términos específicos del tema restando frecuencia estándar. |
| `iolib/iolib.go` | E/S: `FileExists`, `CopyFileContents`, `String2file` (sobrescribe), `String2fileAppend` (añade), `File2string`. |
| `stringlib/stringlib.go` | `RmNewLines`, `IsNumeric`, `Test`. |
| `storelib/` | **Capa de caché intercambiable** (nueva, 2026-11-17). Interfaz `Store` + backends SQLite (por defecto) y Redis. Ver §2.9. |
| `redislib/redislib.go` | Wrapper redigo (pool `MaxIdle:80`, `MaxActive:12000`, TCP `:6379`). `Ping/Set/Get/Exists/SetInterface/GetInterface`. Ya no hace `panic`/`log.Fatal` si Redis cae: devuelve error o valor vacío. Solo lo usa `storelib/redis.go`. |
| `profiles/*.yaml` | 22 perfiles temáticos (ver §2.5). **Ojo:** el programa los lee del directorio de trabajo, no de `profiles/` (hay que copiarlos a la raíz). |
| `logs/` | Todos los logs y el corpus (§2.6). Ignorado por Git. |
| `output/` | `ranking-URL.csv`, `LPool.json`, `domainCounter.json`, `corpusFrequencies.txt`, `corpusNoEngFrequencies.txt`. |

**Dependencias clave (go.mod):** `whatlanggo`, `redigo`, `collectlinks`, `html2text`, `viper`,
`snowball`, `goPackages`, `go-cache`, **`modernc.org/sqlite v1.31.0`** (puro Go, sin CGO;
v1.32+ ya pidió Go 1.21). Eliminadas por `go mod tidy`: `go-lang-detector`, `prose/v2` (NER).
`go-cache` sigue en go.mod pero solo lo usan `cacheInit()/cacheSave()`, desactivados.

### 2.3 Flujo de `main()` (crawler.go)

1. Crea `./logs` y `./output` si faltan.
2. `redislib.Ping()` — Redis es obligatorio.
3. `yamlInitGeneral()` → `yamlInitProxy()` → `yamlInitSpecific()`.
4. `corpusfreqlib.Init()` (frecuencias del inglés).
5. `linkBootstraping()` (URLs semilla) → `csvInit()` → trunca `corpusCuratedText.log`.
6. Bucle infinito `doNextLink()` hasta agotar el pool.

**`doNextLink()` (1082-1406), corazón del proceso:**
- Descarga vía Redis (cached u online), con timeout de 8 s y proxy opcional.
- Por párrafo: detección de idioma (`whatlanggo`, solo `"English"`), limpieza de HTML/URLs,
  ratio de stopwords (descarta < 0,1).
- `curatedContent`: párrafos ≥ 200 chars con ratio de stopwords 0,1–0,38.
- Filtros: `minDocLen`/`maxDocLen`, firma de unicidad (7 tokens más frecuentes),
  `scoreThreshold`, ratio `maxFreq/numWords` 0,002–0,1.
- Escribe el corpus y el CSV; cada 50 enlaces guarda frecuencias y volcados JSON.

**Prioridad de enlaces (`getNextLink`, 384-423):** `Count² / (domainCounter[dom]+1)`; las semillas
van primero. Un dominio con > 6 fallos se descarta (`domainHadFailed`). Se ignoran enlaces internos
del mismo dominio de segundo nivel (`addLinksOf`).

### 2.4 Configuración y ejecución

```shell
# Requisito: Redis corriendo
sudo systemctl start redis && redis-cli ping    # PONG

# Desde la raíz del proyecto (crawler.go y crawler.yaml deben estar ahí):
cp profiles/crypto.yaml ./
go run crawler.go crypto          # único argumento: nombre del perfil YAML sin extensión
# otros: neutral, litecoin, machineLearning, stressDetection, debug...
```

- **Sin variables de entorno ni flags.** Config 100% YAML + `os.Args[1]`. Si falta el argumento → panic.
- **`crawler.yaml` (general):** dominios baneados (facebook, twitter, reddit, google, youtube,
  etherscan, wikipedia, github, pinterest...), tokens baneados (`login|signup|search|pdf` +
  extensiones), stopwords, `downloadTimeout: 8`, `addUrl` de indexador externo (comentado).
- **Perfil YAML:** `curatedDomains`, `linkOk`, `regexRankingKeywords`, `minDocLen`, `maxDocLen`,
  `scoreThreshold`, `bootstrapingLinks`.
- **`proxy.yaml`:** opcional (`proxyHost`, `proxyUser`, `proxyPass`), no presente.
- **No hay LICENSE, Makefile, scripts .sh, Dockerfile ni tests formales.**

### 2.5 Perfiles / categorías de corpus (22 en `profiles/` + 3 sueltos)

Cripto: `crypto`, `litecoin`, `blockchain`, `crypto-top80`, `crypto-duckduckgo`,
`crypto-substack`, `cryptocurrency-Wikipedia`, `cryptoPriceForecast`, `CryptoWar`, `Polkadot`
(+ `Polkadot-Wikipedia`, `Polkadot-Linkedin`), `Web3-Bing`, `Decentralized-Bing`, `dao`.
Salud: `covid19`, `vaers` (Vaccine Adverse Events Reporting System), `covid-vaers`,
`covid-vaers-substack`. Otros: `machineLearning`, `revolution` (Freedom Convoy/Ucrania), `1`
(mixto). Sueltos: `neutral` (inglés general), `debug`, `stressDetection` (fNIRS/EEG/deporte).

Idioma: solo inglés (ISO 639-3 `eng` de `whatlanggo`). No hay parámetro para cambiarlo.

### 2.6 Sistema de logs

| Log | Generado en | Comportamiento |
|---|---|---|
| `linksNotOk.log` | `crawler.go:373` | **Acumulativo (append)**. Links que no casan `regexLinkOk` (keywords/dominios del perfil). 40,2 M líneas, 1,6 GB. |
| `linksNotOk.additional.log` | `crawler.go:358` | **Acumulativo**. Fallos del regex estricto (§2.7). 5,55 M líneas, 306 MB. |
| `linksNotOk.ParseRequestURI.log` | `crawler.go:345` | **Acumulativo**. Fallos de `url.ParseRequestURI`. 260 k líneas, 11 MB. |
| `corpusCuratedText.log` | `crawler.go:1264`, `:1533` | **Se trunca al arrancar**. El corpus curado (URL + texto + `----`). |
| `corpusCuratedText-*.log` | — | **Renombrados a mano** de ejecuciones anteriores (Crypto, Litecoin, VAERS, etc.). No los genera el código. |
| `download.log` | `crawler.go:1041` | **Se trunca**. Una línea por descarga (Requested/Ok/errores). |
| `domainFailed.log` | `crawler.go:504`, `:1056` | **Se trunca**. Dominios con error de transporte; > 6 apariciones → descartado. |
| `cgi.log` | `crawler.go:449`, `:1051` | **Se trunca**. Links a los que se quitó la query string para canonicalizar. |

**Código inactivo / deuda técnica:** bloque de n-gramas globales tras `if false`, `bigramsOf()`
comentado, `cacheInit()/cacheSave()` (gob) sin uso, NER y stemming desactivados,
`logDomainFailed` declarado sin uso, `corpusfreqlib/test/goCorpusFreqTest.go` con import de
distinta capitalización (probablemente no compila).

### 2.7 Embudo de validación de links (`linkSeemsOk`, 333-382)

1. **Longitud:** < 13 o > 256 → descartado sin log.
2. **`url.ParseRequestURI`** (343) → fallo: `linksNotOk.ParseRequestURI.log`.
3. **Regex estricto adicional** (355, añadido en `a57c1ee` "Additional URL validator" para evitar
   panics con URLs tipo `http://(http://...`):
   `(?i)(^http(s)?://[a-z0-9-]+(\.[a-z0-9-]+)*(:[0-9]+)?(\/[a-z0-9\-\_\/\.\+\%\(\)\~\@]*)?$)`
   → fallo: `linksNotOk.additional.log`.
4. **Filtro del perfil** (`regexLinkOk` = `linkOk|curatedDomains`) si `avoidCustomCheck=false`
   → fallo: `linksNotOk.log`.
5. **Listas negras** de `crawler.yaml` (`isBanned`) y dominio vacío → descartados sin log.

### 2.8 Migración a Go 1.20 y compilación (2026-11-17)

- Go instalado en el sistema: **1.20.3** (`/usr/bin/go`), con `GOPATH=/home/jips/go`.
- **Cambios realizados:**
  - `go.mod`: `go 1.13` → **`go 1.20`**.
  - `go mod tidy`: eliminó dependencias declaradas pero sin uso (`go-lang-detector`, `prose/v2`)
    y separó las indirectas en un segundo bloque `require`. `go.sum` regenerado.
  - `corpusfreqlib/test/goCorpusFreqTest.go:11`: corregido el import
    `goCrawler/goCorpusFreqLib` → `goCrawler/corpusfreqlib` (capitalización errónea; rompía
    `go build ./...` incluso con Go 1.13).
- **Verificación:** `go build ./...` ✅ · `go vet ./...` ✅ (sin avisos) ·
  `go build -o crawler.bin crawler.go` ✅ (binario ~9,5 MB, generado en `/tmp/opencode/`).
- **Smoke test** con `go run crawler.go debug` (Redis 5.0.7 arrancado temporalmente): el
  programa carga YAML, conecta con Redis (`PING → PONG`), inicializa el corpus inglés
  (669.418 tokens BNC + 27.447 contemporáneo, factor 133,85) y arranca el bootstrapping
  correctamente. Sin errores de runtime. Redis se detuvo después y los logs vacíos creados
  por el test se eliminaron, dejando `logs/` como estaba.
- **Nota:** el smoke test inicial se hizo con Redis, pero desde §2.9 **Redis ya no hace falta**
  por defecto. El perfil `debug` usa una semilla en checo (`euractiv.cz`) que el filtro de idioma
  descarta; es solo de prueba.

### 2.9 Cache desacoplada: SQLite por defecto, Redis opcional (2026-11-17)

Decisión del usuario: **SQLite como backend local**, se descartan todos los datos antiguos de
Redis (no se migra nada), se reciclan código y estructuras, y Redis debe seguir funcionando
pero sin ser obligatorio.

**Diseño implementado:**
- Paquete nuevo **`storelib/`**:
  - `storelib.go`: interfaz `Store` (`Exists/Get/Set/Close/Name`), constantes `BackendLocal` y
    `BackendRedis`, y factory `Init(backend, path)`. Backend desconocido → error explícito.
  - `sqlite.go`: tabla `cache(key TEXT PRIMARY KEY, value TEXT NOT NULL, updated_at DATETIME)`.
    Driver **`modernc.org/sqlite v1.31.0`** (puro Go, sin CGO). WAL + `synchronous=FULL`
    (sync en disco) + `busy_timeout=5000`, `SetMaxOpenConns(1)`. `Set` usa
    `INSERT ... ON CONFLICT(key) DO UPDATE`. La BD se crea sola en `./cache/`.
  - `redis.go`: adaptador delgado que envuelve `redislib` sin cambiar su API.
  - `store_test.go`: tests de ambos backends (persistencia tras cerrar/reabrir, update,
    backend inválido y Redis caído sin panic). `go test ./...` ✅.
- **`crawler.yaml`:** nuevo `cacheBackend: local` y `cachePath: ./cache/cache.db`. Viper lee
  ambos en `yamlInitGeneral()`; si faltan, aplica esos defaults.
- **`crawler.go`:** `downloadCached()` usa la variable global `cacheStore` en lugar de llamar a
  `redislib` directamente. `main()` inicializa el store **después** de cargar el YAML y hace
  `defer cacheStore.Close()`; si falla la inicialización, mensaje + `os.Exit(1)`.
- **`redislib/redislib.go`:** eliminados `panic()` en `Dial` y `log.Fatal` en `Exists/Get/
  SetInterface/GetInterface`. Con Redis caído devuelve error/valor vacío, no mata el proceso.
  Se quitó el `fmt.Printf("DEBUG: %s", value)` que volcaba la página cacheada.
- **Formato de datos sin cambios:** clave `cache_<URL>`, valor JSON de `CachedData{Content, Links}`.
  Los `gob.Register`/`cacheInit`/`cacheSave` antiguos quedan intactos pero sin uso.

**Verificación:** compila y pasa `go vet` y `go test ./...` con Redis **parado**; ejecución real
con `cacheBackend: local` crea `cache/cache.db` y no menciona Redis; con `cacheBackend: redis` y
servidor arrancado, usa Redis (`Cache backend: Redis`). Estado final limpio: Redis parado,
`logs/` y `cache/` vacíos.

---

## 3. Resumen de `linksNotOk.log` (1,6 GB) — ***EL LOG YA BORRADO***

> Analizado el 2026-11-17 antes de borrarlo. **40.282.346 líneas**, fecha de último
> Write: 2023-06-14. Es el "vertedero" de enlaces que **pasaron** los filtros de longitud y
> sintaxis pero **no casaron** las palabras clave/dominios curados del perfil activo.

- **Formato:** una URL por línea, sin cabeceras ni timestamps. Longitud media 40 caracteres.
- **Esquemas:** HTTPS 33,9 M (84,2%), HTTP 6,3 M (15,6%), protocolo-relativas `//...` 9.078,
  otros 5.213. Solo 117.829 líneas (0,3%) llevan `?`, ninguna `#`, 41.837 con %-encoding.
- **174.650 hosts distintos**, pero altísima concentración. Top dominios de 2º nivel:
  `addtoany.com` 2,34 M, `wordpress.com` 1,96 M, `geckoterminal.com` 1,61 M,
  `clinicaltrials.gov` 1,29 M, `linkedin.com` 1,27 M, `pinterest.com` 929 k,
  `tumblr.com` 825 k, `digg.com` 532 k, `addthis.com` 463 k, `whatsapp.com` 426 k,
  `getpocket.com` 402 k, `vk.com` 345 k, `wa.me` 329 k, `stumbleupon.com` 322 k.
  TLDs: `.com` 28,3 M, `.org` 2,2 M, `.gov` 2,0 M, `.me` 950 k, `.net` 600 k, `.ru`/`.uk` 410 k.
- **Naturaleza del contenido:** abrumadoramente **chrome de navegación y widgets de redes
  sociales** que aparecen en el pie/lateral de casi cualquier página: botones de compartir
  (AddToAny, AddThis, Pocket, Digg, StumbleUpon, WhatsApp, Telegram, VK), enlaces de
  redes (LinkedIn, Pinterest, Tumblr, Twitter), `wp-admin/customize.php` de blogs WordPress,
  redirects afiliados (`go.redirectingat.com`, `adjust.com`, `appsflyer.com`), y enlaces
  legales/de Wikimedia (`//foundation.wikimedia.org/wiki/Privacy_policy`, Terms of Use).
- **Las primeras líneas** son enlaces de Wikipedia/Wikimedia/Crypto (perfil cripto); **las
  últimas** son NLM/NIH/HHS/PubMed (perfiles covid/VAERS). El fichero es un **acumulado de
  muchas ejecuciones de perfiles distintos concatenadas**, sin separadores entre ejecuciones.
- **Duplicados masivos:** la misma URL se registró miles de veces (p. ej.
  `https://www.youtube.com/user/NLMNIH` 4.321 veces, `https://www.hhs.gov/` 5.710 veces).
  Causa probable: el crawler reprocesa páginas ya en caché de Redis en cada ejecución y vuelve
  a appendear los mismos enlaces boilerplate. **No hay deduplicación ni límite de tamaño.**

---

## 4. Resumen de `linksNotOk.additional.log` (306 MB) — ***EL LOG YA BORRADO***

> Analizado el 2026-11-17 antes de borrarlo. **5.550.422 líneas**, último Write 2023-06-14.
> Es el "vertedero" de valores de `href` que **fallaron el regex estricto** de la capa 3 (§2.7).
> Se introdujo en 2022-03-12 para evitar panics; muestra que los `<a href>` del HTML distan
> mucho de ser URLs HTTP absolutas.

- **Formato:** una línea por enlace rechazado. Longitud media 57 caracteres.
- **Desglose por tipo de fallo:**
  - **Rutas relativas: 4.406.922 (79,4%)** — de ellas 3.242.383 son rutas simples tipo
    `/tos`, `/privacy`, `/price/bittorrent/`, `/en-gb/...`; 1.055.661 son relativas con
    caracteres especiales; 108.878 son solo query (`/?term=Selimovic+A&cauthor_id=...`, típico
    de PubMed/NCBI).
  - **URLs con query string: 967.665 (17,4%)** — sobre todo *share links* con el payload
    percent-encoded: `facebook.com/sharer/sharer.php?u=...`, `twitter.com/intent/tweet?...`,
    `reddit.com/submit?...`, `gab.com/compose?...`, `gettr.com/share?...`, `parler.com/new-post?...`,
    `tumblr.com/widgets/share/tool?...`.
  - **`javascript:void(0)` / `javascript:void(0);`: 58.741 (1,1%)**.
  - **`mailto:` y `tel:` y similares: ~82.178 (1,5%)** (p. ej. `mailto:feedback@forbes.com`,
    `tel:+44 (0)20 7843 2320`).
  - HTTP 155.053, HTTPS 849.175 (el resto son los esquemas no-http).
- **Hosts de origen más frecuentes:** `twitter.com` 185.847, `www.blogger.com` 78.842,
  `www.facebook.com` 66.489, `www.ncbi.nlm.nih.gov` 33.139, `www.youtube.com` 31.627,
  `www.linkedin.com` 30.541, `scholar.google.com` 26.372, `medium.com` 19.363,
  `cran.r-project.org` 15.278, `www.reddit.com` 11.885, `web.archive.org` 8.934,
  `bitcointalk.org` 6.733, `dx.doi.org` 5.997.
- **Lectura:** este log es la prueba de que el regex protector hace un trabajo real: sin él,
    el crawler intentaría descargar `javascript:void(0)`, rutas relativas sin dominio y
    URLs con `?`/`&`/`=`, que romperían `ParseRequestURI` o generarían peticiones inútiles.
    Los últimos ejemplos (queries MeSH de PubMed, Google Maps de NIH) confirman que también
    captura enlaces de navegación interna legítima que simplemente no son absolutos.

---

## 5. Conocimiento y pautas extraídas de la experiencia de funcionamiento

1. **El 99% de los enlaces extraídos de una web son ruido temático.** El 1,6 GB de
   `linksNotOk.log` está dominado por widgets de compartir, trackers y enlaces legales. La
   heurística realmente discriminante no es la descarga, sino la **whitelist de
   `linkOk`/`curatedDomains` del perfil**. Invertir en mejores keywords/dominios curados rinde
   más que optimizar la descarga.
2. **Los logs acumulativos crecen sin control y son una deuda operativa.** Los tres
   `linksNotOk*.log` suman ~1,9 GB de un total de 2,1 GB en `logs/`. El patrón
   `String2fileAppend` + reprocesado de páginas en caché Redis produce duplicados masivos
   (miles de veces la misma URL). **Son regenerables y están en `.gitignore`: borrarlos es
   seguro** y libera ~1,9 GB. Si se quiere conservar su valor diagnóstico, lo correcto es
   guardar solo enlaces únicos, con rotación por tamaño o muestreo.
3. **La robustez se construyó por capas, a golpes de fallos reales.** El orden de los commits
   lo demuestra: `9196e93` añadió `ParseRequestURI` (2022-03-12), `a57c1ee` añadió el regex
   estricto el mismo día tras aparecer URLs mal formadas, `6917c85` refinó `linkSeemsOk`
   (2022-03-27). Cada capa del embudo (§2.7) corresponde a un tipo de error encontrado en
   producción. **No eliminar capas "por parecer redundantes".**
4. **El perfil importa más que el algoritmo.** El mismo código produjo corpus distintos
   (Crypto, Litecoin, VAERS, Revolution...) solo cambiando el YAML. Las firmas de
   `corpusCuratedText-*.log` son renombrados manuales: conviene versionar/perfilar mejor el
   nombre de salida si se automatiza.
5. **Dominios "inesperados" aparecen mucho:** `clinicaltrials.gov` (1,29 M rechazos) y
   `geckoterminal.com` (1,61 M) indican que los perfiles Covid/VAERS y Crypto respectivamente
   encuentran estas fuentes una y otra vez. Son **candidatos a añadir a `curatedDomains`** o a
   afinar `linkOk` si se quiere aprovechar ese tráfico en vez de descartarlo.
6. **El corpus es bilingüe-limitado por diseño:** solo inglés, detectado párrafo a párrafo;
   cualquier otro idioma se descarta. Cambiar esto requiere tocar `crawler.go:1144-1149`.
 7. **La caché (Redis antes, SQLite ahora) acelera pero también dispara el ruido:** cada
    ejecución reextrae links de páginas cacheadas, por lo que los logs de rechazo crecen aunque
    no se descargue nada nuevo. El backend cambió (§2.9), el comportamiento no.

---

## 6. Recomendaciones si se continúa el desarrollo

- ~~**Borrar** `logs/linksNotOk.log`, `logs/linksNotOk.additional.log` y
  `logs/linksNotOk.ParseRequestURI.log` (~1,9 GB libres).~~ **HECHO por el usuario el
  2026-11-17**: `logs/` está vacío. El conocimiento está preservado en §3 y §4.
- Si se quiere evitar que vuelvan a crecer: deduplicar antes de appendear (tabla SQLite de links
  ya rechazados, reutilizando `storelib`), o limitar el log a las primeras N apariciones por host.
- Documentar/versionar los perfiles (hoy están en `.gitignore`) para no perder la configuración
  que generó cada corpus.
- ~~Añadir un `go test` mínimo~~ **HECHO**: `storelib/store_test.go` cubre ambos backends
  (`go test ./...`). Falta un `Makefile` con `run`/`build`.
- Revisar el bloque de n-gramas (`if false`) y el NER (`prose/v2`) si se reactiva el análisis.

---

## 7. Notas del usuario (sección viva — añadir aquí)

_(Espacio reservado. Cada vez que me des una instrucción, decisión de diseño o dato importante,
lo registro aquí y ajusto las secciones anteriores si hace falta.)_

- **2026-11-17:** El usuario ha pedido este fichero de contexto. Ha indicado que los dos logs
  grandes (`linksNotOk.log` y `linksNotOk.additional.log`) **van a ser borrados** tras el
  análisis, y que aquí debe quedar registrado el conocimiento útil extraído de ellos. Ir
  aprendiendo y ampliando este documento a medida que el usuario aporte más información.
- **2026-11-17:** El usuario confirmó que **ya limpió `logs/`** (vacío) y pidió **actualizar el
  código a la última versión de Go instalada (1.20.3) y compilarlo**. Ver §2.8: migración
  completada, `go mod tidy` aplicado, import del test corregido, build y vet OK, smoke test
   con Redis correcto. Preferencia a respetar: mantener el entorno limpio tras las pruebas
   (Redis parado y sin logs residuales).
- **2026-11-17:** Tras analizar la viabilidad, el usuario eligió **SQLite** como backend local.
   Confirmó que **los datos existentes en Redis se pueden borrar todos** (sin migración) y que se
   trabaja **desde cero reciclando código y estructuras**. Redis se mantiene como opción pero ya
   no es obligatorio. Implementación completa en §2.9. Preferencias a respetar: sin datos
   heredados, entorno limpio tras probar, y documentación actualizada.
 - **2026-11-17:** El usuario pidió una **prueba real** del buscador con el tema *astrophotography
   image stacking*, **todo en inglés**. Ver §8: perfil nuevo, dos pasadas, resultados y aprendizajes.

### Políticas habituales de trabajo (reglas permanentes)

- **Refrescar SIEMPRE la lista de tareas en la columna derecha de OpenCode** (regla reiterada por el
  usuario el 2026-11-18, 2026-11-19): usar la herramienta de tareas (`todowrite`) **en CADA turno**,
  incluso en respuestas cortas, y antes del primer tool call de fondo; mantener **exactamente una**
  tarea en `in_progress`; actualizar los estados **en tiempo real, paso a paso**, nunca agrupar
  completados al final; añadir tareas nuevas en cuanto aparecen instrucciones; dejar todo completado
  (o con el bloqueo explicado) antes de responder. El usuario quiere ver **la evolución del trabajo
  en la columna derecha** en cada momento, no solo el resultado final. No responder sin haber
  refrescado la lista. Revisado el 2026-11-19: `~/.config/opencode/opencode.jsonc` no bloquea
  `todowrite` y no hay agentes con permisos que lo nieguen; el estado se escribe correctamente, así
  que si el panel no se ve es un asunto de renderizado del TUI (puede requerir reiniciar OpenCode).
- **Fallo detectado 2026-11-18**: hubo un turno (implementación de la caché persistente de robots.txt)
  en el que **no se llamó a `todowrite` en absoluto**, por eso la columna derecha quedó desactualizada.
  Regla reforzada: **ningún turno puede terminar sin una llamada a `todowrite`**, aunque la respuesta
  sea corta o el trabajo parezca trivial. Si el turno solo documenta, la lista también debe crearse
  y marcarse completada.
- **Permisos de OpenCode (regla permanente, 2026-11-18)**: OpenCode tiene **permiso de lectura y
  escritura** tanto en el directorio en el que está trabajando como en `/tmp`; no debe pedir
  confirmación para leer/editar ficheros en esas rutas. Configurado también en
  `~/.config/opencode/opencode.jsonc` (sección `permission`: `read`, `edit` y `external_directory`
  con `/home/jips/datos/datos-juan/proyectos/**` y `/tmp/**` en `allow`). Si el usuario trabaja en
  otra carpeta, añadirla a esa lista. Los cambios de configuración requieren reiniciar OpenCode.
- Todo el **código y la documentación del repo en inglés** (este `context-ai.md` es el diario
  histórico en español; se mantiene en ambas copias).
- Mantener el entorno limpio tras las pruebas (Redis parado, perfiles copiados a la raíz borrados).
- Los ficheros de salida de cada ejecución se graban en `logs/<Perfil>-YYYY-MM-DD-ZHH-MM-SS.txt`.

---

## 8. Prueba real: `astrophotography image stacking` (2026-11-17)

**Objetivo:** verificar que el crawler encuentra información útil sobre un tema concreto, en
inglés. Perfil nuevo **`profiles/astrophotography.yaml`** (copiado a la raíz para ejecutarlo,
según la convención del proyecto). Contenido 100% en inglés:

- `curatedDomains`: wikipedia, siril.readthedocs.io, pixinsight.com, skyatnightmagazine.com,
  cloudynights.com, astrobackyard.com, deepskystacker.free.fr, nasa.gov, stsci.edu, arxiv.org...
- `linkOk`: `stack|stacking|astrophotograph*|astronom*|pixinsight|siril|registax|autostakkert|
  deepsky|subframe|registration|integration|calibration|...`
- `regexRankingKeywords`: ~70 términos (`stack`, `registration`, `integration`, `subframe`,
  `signal-to-noise`, `dither`, `drizzle`, `fwhm`, `pixinsight`, `siril`, `wbpp`, `sigma clipping`...)
- `minDocLen: 50`, `maxDocLen: 100000`, `scoreThreshold: 20`.

**Pasada 1** (9 semillas): 11 descargas, **5 documentos** en el corpus. Tres semillas
(`en.wikipedia.org/wiki/Image_stacking`, `/wiki/Astrophotography`, `urbanastrophotography.com`,
`astroguide.starlust.de`) devolvieron páginas de ~125-180 chars y **0 enlaces** (bloqueo/JS).

**Pasada 2** (11 semillas, sustituyendo las muertas por `astrobackyard.com` y `cloudynights.com`):
64 enlaces procesados (57 `[ONLINE]` + **7 `[CACHE]` sirvidos por SQLite**), **29 documentos**,
corpus de 934 líneas / 272 KB, `ranking-URL.csv` con 29 filas. `corpusFrequencies.txt` (67 KB) y
`corpusNoEngFrequencies.txt` (48 KB) regenerados correctamente (el filtro de contraste intercorpus
funciona: `image 310, stacking 102, frames 101, histogram 95, astrophotography 78, siril 57,
deepskystacker 35, pixinsight 32`). **Cero textos no ingleses** detectados.

Mejores documentos (score): Sky at Night stacking guide 129,6 · Miklosoft stacking & rejection
117,2 · Siril tutorials 113,2 · DeepSkyStacker live 107,7 · astrobackyard Siril 101,6 ·
Siril docs stacking 100,8. Distribución: 5 con score ≥100, 14 en 60-99, 4 en 40-59, 6 <40.

**Aprendizajes operativos:**
1. **Las semillas determinan el resultado.** Las páginas que devuelven ~125 chars son bloqueos;
   conviene descartarlas y usar dominios amigables con el crawler (`astrobackyard.com`,
   `cloudynights.com`, `siril.org`, `siril.readthedocs.io`, `skyatnightmagazine.com`,
   `deepskystacker.free.fr`, `miklosoft.se`, `astro.bharrat.net` funcionan bien).
2. **La caché SQLite hizo su trabajo:** 7 aciertos en la segunda pasada sin Redis.
3. **El `score` no es relevancia pura:** `rankingByKeywords` divide por `sqrt(1+#tokens)`,
   así que documentos largos y muy relevantes (p. ej. Siril GHS, 6991 chars) puntúan bajo.
   Subir `scoreThreshold` por encima de 40 recortaría también buenos documentos.
4. **Ruido residual esperable:** patreon, weather.com (iPhone), Adobe Camera RAW y itelescope
   se colaron con scores 42-58. Un `curatedDomains` más estricto o un `linkOk` menos genérico
   (`image` es muy amplio) lo reduciría.
5.    El comando fue `go run crawler.go astrophotography` desde la raíz del proyecto, sin Redis.

**Estado:** resultados de la prueba conservados en `logs/corpusCuratedText.log`, `output/` y
`cache/cache.db` (1,1 MB) para inspección. Semillas originales en `profiles/astrophotography.yaml`.

### 8.1 Continuación con la versión del roadmap bloque 1 (2026-11-17)

Tras hacer push de `eea3110` (movimiento de perfiles a `profiles/` + bloque 1), se repitió la
investigación con la nueva versión:

- **Pasada 2 repetida (idéntica a la original):** 29 documentos, 65 `[ONLINE]`, corpus de 934
  líneas. Resultado **idéntico** al de la versión anterior → buena señal de regresión tras quitar
  código muerto, añadir `io.LimitReader` y deduplicar logs.
- **Pasada 3 (semillas ampliadas):** se añadieron 11 fuentes nuevas descubiertas en el propio pool
  y por búsqueda web (PixInsight tutorials, discuss.pixls.us, Astro Pixel Processor, RegiStax,
  AutoStakkert, Sky & Telescope). Resultado: **34 documentos** (corpus 1125 líneas), 5 nuevos:
  `PixInsight M31-Ha`, `Master Calibration Frames`, `StarAlignment distortion`, el tutorial de Siril
  en pixls.us y el índice de siril.readthedocs.io. **65 aciertos de caché SQLite** y solo 11
  descargas online → el backend local reduce muchísimo el tráfico en pasadas sucesivas.
- **Conocimiento temático extraído del corpus:** stacking = integrar subexposiciones alineadas para
  subir la relación señal/ruido (∝√N); flujo estándar *calibración (bias/dark/flat) → registro →
  integración*, con dithering, drizzle, FWHM, sigma clipping y Winsorized Sigma Clipping como
  técnicas recurrentes. Frecuencias temáticas de la pasada 2: `image 310, stacking 102, frames 101,
  histogram 95, siril 57, deepskystacker 35, pixinsight 32`.

**Dos bugs descubiertos durante la investigación:**
1. **Deduplicación entre ejecuciones:** el `map` de `logRejectedLink` se reiniciaba en cada arranque,
   así que los duplicados volvían a aparecer en pasadas sucesivas. **Corregido**: `initRejectedLinkSets()`
   carga las líneas ya existentes de los tres logs al arrancar y se normaliza con `TrimSpace` tanto
   al cargar como al escribir. Verificado: tras tres ejecuciones consecutivas los totales dejaron de
   crecer (422/127/206 líneas, todas únicas).
 2. **Frecuencias desactualizadas (no corregido, añadido al roadmap como ítem 18):** el volcado de
    `corpusFrequencies.txt` ocurre dentro de `doNextLink()` solo si el documento pasa *todos* los
    filtros y `numLinksProcessed % 50 == 0`. En la pasada 3 solo se volcó en el índice 0, por lo que
    los ficheros describen un único documento (`the 18`) aunque el corpus tenga 34. Mover el volcado
    al final de cada iteración o al terminar la ejecución.

---

## 9. Nueva línea de investigación: SkyStacker (2026-11-17)

El usuario pidió **focalizar la investigación** en
`/home/jips/datos/datos-juan/proyectos/astronomia/SkyStacker/research/`.

- Es la carpeta de investigación de **SkyStacker**, un motor CLI Go *local-first* (v0.4.1 → v0.5.0)
  para calibración, registro, apilado, post-procesado e identificación astronómica.
- Estructura: `01-architecture-and-core-engine/` (pipeline de 9 pasos, concurrencia, `sync.Pool`,
  SIMD/Swiss Tables), `02-astrometry-and-identification/` (plate solving, HEALPix, Gaia DR3,
  cross-matching, anomalías), `03-reference-and-literature/` (50 fuentes, glosario de 100 términos),
  más `04-calibration-and-self-flat.md`, `05-research-deep-dive-2026-11-16.md` y los outcomes de
  Gemini Deep Research.
- Entrada recomendada: `RESEARCH_SYNTHESIS.md`. Temas clave para el crawler: sigma-clipping,
  SNR ∝ √N, FWHM/eccentricidad, master dark/flat/bias, drizzle, WCS/SIP, HEALPix, Gaia DR3,
  XISF/FITS, GHS, RC-Astro, ASTAP, Siril, PixInsight.
- **Pendiente:** crear un perfil de crawler alineado con estas líneas (no solo "image stacking"
  genérico) y guardar los resultados en esa línea de investigación.

## 10. Uso de CPU y política robots.txt (2026-11-17)

- **Núcleos:** 8 (`nproc` = 8, `GOMAXPROCS` por defecto = 8).
- **Uso real medido** con `/usr/bin/time -v` sobre el binario compilado, 1:46 de duración:
  User 24,78 s + System 0,88 s = 25,66 s de CPU → **24 % de un solo núcleo** (0,24/8).
  **El crawler NO usa los 8 núcleos**: es secuencial (un solo `doNextLink` por iteración); la
  única gorutina extra es la del indexador externo `addUrl`, desactivada por defecto. El runtime
  puede usar hilos para GC/scheduler, pero no hay trabajo paralelo. Para usar los 8 núcleos haría
  falta el ítem 14 del roadmap (worker pool + rate limiting por dominio).
- **Política robots.txt implementada** en el paquete nuevo `robotspolicy/` (dependencia
  `github.com/temoto/robotstxt v1.1.2`):
  - `Policy.Allowed(url)` descarga y **cachea por host** el robots.txt, aplica `FindGroup(userAgent)`
    y `group.Test(path)`. Si no existe (404) o hay error de red → **fail open** (permitir).
  - `Policy.Wait(url)` aplica el `Crawl-delay` del grupo entre peticiones al mismo host.
  - Config en `crawler.yaml`: `respectRobots: true` (default) y
    `userAgent: "goCrawler/1.0 (+https://github.com/computerphysicslab/goCrawler)"`.
  - En `downloadCached()`, antes de `download()`, se comprueba `Allowed`; si está bloqueado se
    escribe en `logs/robotsBlocked.log` (deduplicado) y se devuelve error. Si está permitido se
    llama a `Wait` para respetar `Crawl-delay`.
  - Tests en `robotspolicy/robotspolicy_test.go` (allow/block/crawl-delay y fail-open).
  - **Bug corregido**: `robotstxt.Group.CrawlDelay` **ya es** `time.Duration`, así que multiplicarlo
    por `time.Second` producía un `Sleep` de siglos y colgaba el test. Asignación directa.
    `go test ./...` ✅ (incluye el delay de 1 s del test).
- Nota para el usuario: este fichero es el canónico y está en la raíz de `myTools`; se deja
  además una copia en la raíz del repo como `context-AI.md` para que sea visible desde el proyecto.
  **Actualizar ambas copias** cuando se añada información nueva.

## 11. Resultado de la investigación enfocada en SkyStacker (2026-11-17)

- Perfil nuevo **`profiles/skystacker-research.yaml`** (todo en inglés), con 21 semillas extraídas
  del propio catálogo de referencias de `astronomia/SkyStacker/research/` y dominios/keywords de
  astrometría (ASTAP, SCAMP, plate solving, WCS, Gaia, drizzle, FITS, photometría).
- Ejecución con `robots.txt` activo: 73 descargas online, **18 documentos** en el corpus,
  **8 URLs bloqueadas por robots.txt** (ivoa.net, nova.astrometry.net, docs.astropy.org, wcstools,
  flickr) y varios robots.txt inaccesibles tratados con fail-open. La ejecución se cortó por el
  timeout de 240 s con el pool aún no vacío → resultado parcial.
- Fuentes más relevantes: photutils (71,7), Siril drizzle (47,6), DeepSkyStacker technical (43,6),
  CFA drizzle de PixInsight (40,1), ASTAP (36,5), paper original de drizzle del STScI (36,1),
  Astrometry.net docs (35,3), SCAMP (23,9) y Gaia DR4.
- Informe guardado en
  `astronomia/SkyStacker/research/06-crawler-based-source-discovery-2026-11-17.md` (en inglés),
  con método, resultados, observaciones de robots.txt, nota de CPU y seguimiento sugerido.
 - README y roadmap actualizados: política robots documentada, ítem 12 marcado DONE (falta el delay
   genérico configurable por dominio), ítem 18 (frecuencias desactualizadas) sigue pendiente.

## 12. Análisis de paralelización con 8 núcleos (2026-11-17)

**Conclusión:** se puede paralelizar, pero el objetivo no es "usar los 8 cores" sino **solapar
latencia de red**. El trabajo es I/O-bound (24 % de un núcleo medido), así que 8 workers es un
límite razonable, no un requisito; el cuello no es la CPU.

Estado compartido que habría que proteger o dar dueño único (ver tabla en README §"CPU usage"):
`lPool`, `domainCounter`, `uniqueSignature`, `corpusFreqs`, `csvWriter` (no concurrency-safe),
los sets/ficheros de `logRejectedLink`, `cacheStore` (SQLite `SetMaxOpenConns(1)` / Redis una sola
conexión) y `robotsPolicy` (ya tiene mutex, pero `Wait` debería reservar el hueco por host).
El código puro (tokenización, whatlanggo, html2text, collectlinks, ranking, stopwords) no cambia;
config y `corpusfreqlib` son read-only tras init.

Diseño propuesto (roadmap 14, **no implementado**): un dispatcher dueño de `lPool`/`domainCounter`
que alimenta un canal, N workers que descargan y filtran, y un agregador dueño de
`uniqueSignature`, `corpusFreqs`, CSV, corpus y JSON. Añadir rate limit por dominio y validar con
`go test -race`. Ganancia esperada: gran reducción de tiempo wall; el orden de salida cambiaría.

## 13. Comentarios en inglés y autotests (2026-11-17)

- El usuario pidió **comentarios en inglés para cada función** y **autotests**. Regla a respetar:
  desde ahora **todo el código y la documentación del repo en inglés** (`context-ai.md` es la
  excepción histórica en español, por ser el diario de contexto del usuario).
- Se añadieron doc comments en inglés a **todas las funciones** de `crawler.go`, `corpusfreqlib`,
  `iolib`, `stringlib`, `redislib`, `storelib` y `robotspolicy`, incluidas las de test. Verificado
  con un script que comprueba que cada `func` tiene una línea `//` inmediatamente anterior.
- **Autotests nuevos:**
  - `crawler_test.go` (paquete main): 11 tests de funciones puras y estado — `tokenize`,
    `splitParagraphs`, dominios, `rankingByKeywords`, `analyze`, `bestParagraph`, frecuencias,
    n-gramas, `linkSeemsOk`, `domainHadFailed` (sin inyección de regex) y dedup de logs. Usa
    `t.TempDir()` + `os.Chdir` para no tocar los logs reales.
  - **Modo `--selftest`** implementado **dentro de `crawler.go`** (importante: `go run crawler.go`
    compila solo ese fichero, así que `runSelfTests` no puede vivir en otro archivo del paquete).
    Imprime `[PASS]`/`[FAIL]` por check y sale 0 solo si todo pasa; 11 checks ✅.
  - `Makefile`: nuevo target `selftest` (`go run crawler.go --selftest`).
- Nota de implementación: `setupTestConfig()` vive en `crawler.go` (no en `_test.go`) porque lo
  usan tanto los tests como `--selftest`. Las funciones `freq.add` y `analyze` eliminan tokens de
  1-2 caracteres, por eso los tests usan `alpha`/`beta` en vez de `x`/`y`.
 - Verificación: `go build ./...`, `go vet ./...`, `go test ./...` y `go run crawler.go --selftest`
   ✅. Roadmap: ítem 9 marcado DONE.

## 14. Ficheros de sesión y rendimiento de la caché (2026-11-17)

- El usuario pidió que la salida en pantalla se grabe en ficheros tipo
  **`Astrophotography-2026-11-17-Z20-30-33.txt`**. Implementado `startSessionOutput()` en
  `crawler.go`: crea `logs/<Perfil>-YYYY-MM-DD-ZHH-MM-SS.txt` con **timestamp UTC** (prefijo `Z`),
  nombre del perfil capitalizado, y hace *tee* de `os.Stdout` al terminal y al fichero mediante
  `os.Pipe` + `io.MultiWriter`. Se cierra con `defer` al terminar `main`. Verificado: ficheros
  de 28-65 KB con la salida completa (config, `[CACHE]`/`[ONLINE]`, `Done!!!`).
- **Medición del caché SQLite** con el perfil `astrophotography`, dos rondas consecutivas:
  - **Fría** (caché vacía): **194 s**, 0 `[CACHE]`, 76 `[ONLINE]`, 35 documentos, CPU 16 % de un
    núcleo, BD final de **32 MB**.
  - **Caliente** (misma BD): **45 s**, **76 `[CACHE]`**, 0 `[ONLINE]`, los **mismos 35 documentos**,
    CPU 53 % de un núcleo.
  - **Aceleración ≈ 4,3×**, cero peticiones HTTP en la ronda caliente y resultados idénticos.
    La CPU sube porque desaparece la espera de red, pero sigue siendo un solo hilo de trabajo.
- README actualizado con la tabla de sesiones y la tabla fría/caliente. Los ficheros de sesión y
  `cache/cache.db` quedan en `logs/` y `cache/` (ignorados por Git).

## 15. Crawler en background, política de tareas e ítem 8 (2026-11-17)

- El usuario pidió **poner a trabajar el crawler de fondo con la research de SkyStacker** mientras
  se actualizaba el repo y se avanzaba un punto del roadmap.
  - Lanzado con `setsid /tmp/opencode/crawler.bin skystacker-research` (PID 1194186 al arrancar),
    totalmente desacoplado del shell: `nohup ... &` dentro de la herramienta bash murió al cerrarse
    la sesión; `setsid` + binario compilado sí sobrevive. Salida en `/tmp/opencode/skystacker_bg2.log`
    y fichero de sesión en `logs/Skystacker-research-...txt`.
  - **Política añadida a §7**: refrescar SIEMPRE la lista de tareas en la columna derecha de
    OpenCode (todowrite en tiempo real, nunca agrupar completados al final).
- **Roadmap ítem 8 completado** (perfiles desde `profiles/` sin copiar a la raíz):
  `yamlInitSpecific()` usa ahora una instancia local de Viper, busca `./profiles/<name>.yaml`
  primero y hace fallback a `.`; imprime `profile file:` al arrancar. Makefile `run` simplificado
  (sin `cp`). Verificado: `go run crawler.go crypto` cargó
  `.../profiles/crypto.yaml` con la raíz limpia.
- README y roadmap actualizados. Estado del roadmap: **ítems 1–6, 8, 9 y 12 DONE**; pendientes
  7, 10–11, 13–18.

## 16. Keywords del corpus, ítems 10/11 y preparación Ubuntu (2026-11-17)

- **Perfil SkyStacker ampliado**: del `curated text` de la ejecución en fondo se extrajeron keywords
  reales: `solve-field`, `index files`, `quads`, `skymarks`, `xylist/axy`, `source extraction`,
  `healpix/hpx`, `wcs/wcslib`, `projection`, `celestial coordinates`, `fits bintable`, `scale`.
  El corpus era fuerte en plate solving pero débil en apilado avanzado, así que se buscaron URLs
  nuevas (Subframe Selector, PSFSignalWeight, Winsorized Sigma Clipping, ponderación, culling,
  build-index, code.html de Astrometry.net, astroquery, scivision) y se añadieron al perfil
  `profiles/skystacker-research.yaml` junto con sus keywords.
- **Roadmap 10 DONE**: logging estructurado con `golang.org/x/exp/slog v0.0.0-20230801115018-d63ba01acd4b`
  (Go 1.20 no trae `log/slog`; es el mismo código que se incorporó a la stdlib en 1.21).
  Config `logLevel: info`. Contadores de rechazo por motivo y resúmenes `level=INFO msg=progress`.
- **Roadmap 11 DONE**: `crawlState` (pool, domainCounter, processed, uniqueSignature, corpusFreqs)
  serializado a JSON en la clave `state:crawl` de SQLite; `--resume` restaura el estado, no trunca
  corpus ni CSV y continúa desde el índice guardado. Verificado en dos ejecuciones cortas:
  `crawl state restored processed=0 links=22`.
- **Nueva sección del roadmap "Ubuntu / Debian distribution readiness" (ítems 19–27)** con el análisis
  de las cuatro vías (PPA, Snap Store, Universe, Main). Datos clave para el usuario:
  - El proyecto está en **Go**; el caché SQLite es puro Go → factible `CGO_ENABLED=0`.
  - **NO hay fichero `LICENSE`**: el repo es público pero legalmente "todos los derechos reservados".
    Es el bloqueador 19; sin licencia OSI no hay Universe/Main.
  - Hay **12 vulnerabilidades** (5 high, 7 moderate) reportadas por Dependabot → ítem 21.
  - snapcraft recomienda `confinement: strict` + `$SNAP_USER_DATA` porque el crawler escribe en
    `./logs`, `./output`, `./cache` relativos al CWD; hace falta redirigirlos.
  - Orden recomendado: licencia → versión/build reproducible → vulnerabilidades → Snap → PPA.
- Nota: el usuario pregunta por licencia e idioma; responder que es Go y que falta licencia (recommend
  MIT o Apache-2.0) antes de cualquier vía oficial.

## 17. Ciclo ampliado, licencia MIT y análisis de vulnerabilidades (2026-11-17)

- **Ciclo en background**: perfil `skystacker-research.yaml` ampliado a **49 semillas** (nuevas fuentes
  de calibrate/master frames — PixInsight master-frames, Siril manual preprocessing, WBPP, Sky atNight,
  opticalmechanics, miklosoft — y de plate solving/WCS — twirl, tetra3rs, Siril platesolving SIP,
  LSST AstrometryTask, prose, zodiacal) y keywords ampliadas (`sip`, `distortion`, `crossmatch`,
  `centroid`, `tetra`, `zodiacal`, `log-odds`, `verify`, `inlier`). Lanzado con
  `setsid /tmp/opencode/crawler.bin skystacker-research` (PID 1203990), sesión
  `logs/Skystacker-research-2026-11-17-Z20-27-46.txt`.
- **Licencia MIT añadida**: `LICENSE` con copyright 2020-2026 Juan Ignacio Pérez Sacristán, README
  con sección License y roadmap ítem 19 marcado DONE. Falta `debian/copyright` DEP-5 (ítem 24).
- **Análisis de las 12 vulnerabilidades** (vía `gh api .../dependabot/alerts`, CLI autenticada):
  las 12 abiertas son de **`golang.org/x/net`** (5 high, 7 medium), versión actual del 2020.
  `go mod why` → `collectlinks` → `x/net/html`. Tres vías documentadas en el roadmap ítem 21:
  A) subir a x/net v0.33.0 (compatible Go 1.20) arregla 10/12, quedan 2 medium que exigen Go ≥1.23;
  B) subir toolchain a Go ≥1.23 + x/net v0.38.0 arregla las 12 y permite `log/slog` nativo;
  C) sustituir collectlinks por un extractor propio (no recomendado). Pendiente de decidir.
- Nota: las alertas se basan en el `go.sum` del último push; el Working tree actual añade `x/exp/slog`.

## 18. Análisis profundo, tests y vulnerabilidades resueltas (2026-11-17)

- **Vulnerabilidades RESUELTAS**: se instaló Go **1.26.11** vía `golang.org/dl`, se fijó
  `go 1.26.0` + `toolchain go1.26.11`, se subió `golang.org/x/net` a **v0.62.0** y
  `gopkg.in/yaml.v2` a **v2.4.0**, y se sustituyó `golang.org/x/exp/slog` por `log/slog` nativo.
  `govulncheck ./...` → **"No vulnerabilities found"**. El Makefile usa `GO ?= go1.26.11` porque el
  `go` del sistema sigue siendo 1.20.3 y ya no puede leer el `go.mod`.
- **Análisis function-by-function** en `docs/FUNCTION_ANALYSIS.md` (inglés): revisión de todas las
  funciones con hallazgos y propuestas. Destacados: regex compiladas en cada llamada (ítem 28),
  bug en `getNextLink` que incrementa el contador de dominio aunque no haya candidato (29),
  `domainHadFailed` lee todo el log en cada descarga → O(n²) (30), `proxyGet` crea un cliente por
  petición y desactiva keep-alive (31), `doNextLink` de ~330 líneas (32), panics/os.Exit que saltan
  los defers (33), índice O(1) para el pool (34).
- **Tests nuevos**: `iolib`, `stringlib`, `corpusfreqlib`, `isBanned`, contadores de stopwords,
  stemmer, n-gramas, longitudes de URL, contadores de rechazo, round-trip de `state:crawl`,
  nomenclatura del fichero de sesión, cache hit con store falso y servidor HTTP local para
  `proxyGet`/`download`. Verificado con Go 1.26.11.
- **Roadmap**: ítems 21 y 35 DONE; nuevos ítems 28–35, con 28–30 como quick wins de prioridad alta.

## 19. Implementación de roadmap 15 y 28–34 (2026-11-17)

- **15 (ranking)**: `rankingScore` con tres estrategias configurables por perfil (`rankingStrategy:
  keywords|tfidf|hybrid`), normalización por longitud, IDF calculado del corpus aceptado y bloque
  `synonyms: term: a|b|c` para expansión semántica ligera. Default `hybrid`.
- **28**: todas las regex compiladas una vez (constantes a nivel paquete y derivadas de config en
  `compileConfigRegexes()` al arrancar). Ya no hay `regexp.Compile` en el hot path.
- **29**: `getNextLink` no incrementa `domainCounter` si no hay candidato válido (`maxURL == ""`).
- **30**: `failedDomains map[string]int` en memoria, reconstruido del log y persistido en `state:crawl`;
  `domainHadFailed` ya no escanea el fichero en cada descarga.
- **31**: `httpClient` único con `http.Transport` (keep-alive, timeouts, proxy configurable).
- **32**: `doNextLink` descompuesto en `filterParagraphs`, `buildCuratedContent`, `documentSignature`,
  `isUniqueDocument`, `writeDocument`, `dumpFrequencies` y `notifyIndexer`. El volcado de frecuencias
  ahora también se fuerza al terminar (arregla el bug 18). `corpusfreqlib` guarda un índice JSON
  compacto `corpusfreqlib/freq-index.json` (en .gitignore) para no releer ~700k líneas cada arranque.
- **33**: `run() error` + `main` solo gestiona el exit code; `yamlInit*`/`csvInit` devuelven errores;
  `crawlState.Version` rechaza estados incompatibles; cache JSON corrupto se descarta y se redescarga.
- **34**: `linkIndex map[string]int` para búsqueda O(1) de duplicados y `maxLinksPerPage` (default 200).
- Verificado: build/vet/test/selftest con Go 1.26.11 y ejecución real corta sin panics.

## 20. Nuevo ciclo de investigación (2026-11-17, 22:48 UTC)

- Ejecución completa con `skystacker-research` (binario con Go 1.26.11, sin Redis):
  **338 s** (5:38), **217 enlaces procesados**, **52 documentos aceptados**, corpus de 1602 líneas.
- **Caché**: **144 aciertos `[CACHE]`** frente a **25 descargas `[ONLINE]`** → tasa de acierto
  **85,2 %**. 15 URLs bloqueadas por robots.txt y 48 errores de descarga.
- **Contadores de rechazo** (logueados al final): `non_english 19647`, `invalid_url 3129`,
  `stopword_ratio 4880`, `off_topic_url 247`, `too_short 121`, `low_relevancy 35`, `duplicate 9`,
  `robots_blocked 15`. Son recuentos por párrafo/enlace, no por documento.
- **Contenido nuevo**: 51 de los 52 documentos del corpus son nuevos frente a los corpus guardados
  en ejecuciones anteriores. 6 de las 25 URLs descargadas online pasaron los filtros:
  `tetra3rs.dev/tutorials/basic-solve`, `starstax`, `starnetastro.com`, dos entradas de KStars
  (blog de knro) y un documento técnico de Starlink. La mayoría del corpus viene de semillas ya
  en caché (Astrometry.net, Siril, PixInsight, Astro Pixel Processor), pero las fuentes nuevas
  amplían la cobertura hacia **plate solving con tetra3rs, apilado con StarStax/StarNet y
  catálogos KStars**, muy alineadas con `02-astrometry-and-identification/`.
- **Frecuencias temáticas finales** (el volcado forzado al terminar ya funciona, bug 18 cerrado):
  `image 366, frames 215, stars 206, drizzle 147, gaia 122, noise 120, flat 112, fits 107,
  siril 99, stacking 97, cfa 94, astap 93, astrometry 90`.
- Sesión grabada en `logs/Skystacker-research-2026-11-17-Z21-48-15.txt` (72 KB).

## 21. Política: entregar contenido curado, no solo URLs/keywords (2026-11-17)

El usuario lo ha indicado expresamente: **cuando se pase información recopilada por goCrawler a un
tercero (por ejemplo, la carpeta `SkyStacker/research`), no basta con listar URLs nuevas y keywords:
hay que incluir también el contenido descargado y curado** que queda en
`goCrawler/logs/corpusCuratedText.log`. Ese fichero contiene, para cada documento aceptado, la URL,
el texto filtrado (inglés, stopwords, longitud y relevancia) y el separador `----`. Es la parte
realmente útil: las keywords solo resumen de qué habla la página.

Regla permanente: siempre que se exporte un hallazgo del crawler, adjuntar un extracto del texto
curado correspondiente (o el documento completo si es razonable) además de la URL y las keywords.

## 22. Ciclo 5 y contenido curado entregado (2026-11-17)

- Nuevo ciclo con `skystacker-research` y `-workers=2`: **8 s**, **49 aciertos de caché**, **0
  descargas online**, **33 documentos** (32 nuevos frente al corpus previo). CPU **137 %** → los dos
  workers solapan E/S correctamente (no hay 8 núcleos en uso, pero sí >1 core).
- Siguiendo la política de §21, se creó
  `astronomia/SkyStacker/research/08-curated-content-cycle-5-2026-11-17.md` con el **texto curado
  real** de 10 fuentes (measurethepixels, Siril drizzle/platesolving, PixInsight master-frames,
  tetra3rs, astrometry.net build-index/code, hnsky ASTAP, StarStax, miklosoft), extraído de
  `logs/corpusCuratedText.log` con su score. Los documentos 06 y 07 y el README de research
  referencian este fichero.

## 23. Refactorización del roadmap, auditoría senior, GUI y release (2026-11-17)

- `roadmap.md` reescrito: **sección 1 = único lugar de trabajo**, pendientes ordenados por dificultad
  creciente (P01–P10 S, P11–P18 M, P19–P20 L, P21 GUI XL); completados agrupados por versión; 26
  DEFERRED y 27 OUT OF SCOPE como decisiones ya tomadas.
- `docs/SENIOR_AUDIT.md`: auditoría senior con 21 hallazgos nuevos (A1–A7 corrección, B1–B6
  robustez, C1–C4 rendimiento, D1–D5 arquitectura, E1 GUI), registrados como P01–P21.
- `docs/GUI_ANALYSIS.md`: diseño completo del **goCrawler Workbench**. Decisión: UI web local
  embebida (`go:embed`, stdlib HTTP, bind 127.0.0.1, flag `--gui`) en lugar de Fyne/Wails; 5 pantallas
  (dashboard, editor de perfil, monitor de ejecución con SSE, visor de corpus con texto curado y
  copia, y laboratorio de keywords con diff y realimentación al perfil); fases G1–G6.
- `CHANGELOG.md` con hitos v0.9.0 → v1.5.0; tags de versión en el repo.
- `.github/workflows/ci.yml` (matriz Linux/Windows/macOS con Go 1.26.11) y `release.yml` (5 binarios
  + paquete .deb). Verificado localmente: las 5 compilaciones cruzadas y el .deb se generan bien.

## 24. Release v1.5.0 publicado (2026-11-17)

- Commit final `b8f5b6a`, tags v1.0.0…v1.5.0 pusheados.
- Release: https://github.com/computerphysicslab/goCrawler/releases/tag/v1.5.0 con 6 assets:
  linux/amd64, linux/arm64, windows/amd64, darwin/amd64, darwin/arm64 y `gocrawler_1.5.0_amd64.deb`.
- Compilación cruzada y .deb verificadas localmente con Go 1.26.11 antes de subirlas.
- CI en marcha: `.github/workflows/ci.yml` (Linux/Windows/macOS) y `release.yml` (tags v*).

## 25. Corrección CI, About del repo y roadmap v1.6.0 (2026-11-17)

- **CI Windows fallido**: el check de formato fallaba porque la checkout en Windows convertía los
  ficheros a CRLF y `gofmt -l .` los marcaba todos. Corregido con `.gitattributes`
  (`* text=auto eol=lf`) y ejecutando el format check solo en Linux.
- **GitHub About**: descripción actualizada y 20 topics relevantes (golang, crawler, web-crawler,
  text-mining, nlp, corpus, sqlite, redis, fts5, robots-txt, astrophotography, ubuntu, debian, ...).
- **Roadmap v1.6.0**: implementados P01–P14 y P18 (HTTP status/content-type, sufijos TLD,
  sanitización FTS5, loggers fail-fast, CLI con `-config` y argumentos separados, busy_timeout,
  reserva de hueco robots, índice gzip precompilado, Ctrl-C graceful, crawlDelay genérico, poda del
  pool, sets acotados, top-K heap, detección de idioma por documento y tests CLI/top-K).
- Diferidos con decisión registrada: P15, P16, P17, P19, P20 y P21 (GUI: diseño completo en
  `docs/GUI_ANALYSIS.md`, implementación diferida a v2.x).

## 26. CI verde y release v1.6.0 corregido (2026-11-17)

- Segundo fallo de CI en Windows: `TestString2fileAndAppend` no podía limpiar el TempDir porque
  `iolib.String2file` no cerraba el fichero. Corregido (y `File2string` devuelve "" si open falla).
- CI final: **todos los jobs en verde** en ubuntu, windows y macOS (`gh run view` verificado).
- Release v1.6.0 con 6 assets reconstruidos con el fix; se eliminó el .deb sobrante de v1.5.0.

## 27. Herramienta Go del sistema (2026-11-17)

El `go` del sistema era 1.20.3 y no puede leer `go.mod` (`go 1.26.0` + directiva `toolchain`).
Solución aplicada en este equipo: symlink usuario `~/.local/bin/go -> ~/sdk/go1.26.11/bin/go`
(ese directorio va antes que `/usr/bin` en PATH). Verificado: `go version` → 1.26.11 y
`go run crawler.go -query=drizzle` devuelve resultados FTS5 correctamente. Documentado en README.

## 28. Cierre total del roadmap v1.7.0 (2026-11-17)

- P19: `Crawler` struct con toda la configuración, estado, stores y mutexes; eliminadas las ~40
  variables globales mutables. `app = newCrawler()` se inicializa en `run()`.
- P16/P17: `run()` dividido en `prepareCLI`, `setupRuntime` (con cleanup), `crawlLoop` y
  `reportResults`; `validateConfig()` devuelve errores agregados antes de arrancar.
- P15: `StateDirty` evita escrituras redundantes de `state:crawl`.
- P20: estrategia `semantic` con embeddings locales por hashing de trigramas (sin dependencias);
  `hybrid` combina keywords + TF-IDF + semántico.
- P21: GUI web embebida en `crawler.go` (`-gui`, puerto 8765): dashboard con visitas/caché/tiempos,
  editor de perfiles, visor del corpus con texto curado y copia, búsqueda FTS5, laboratorio de
  keywords con realimentación al perfil y arranque/parada de crawls. Verificado con curl.
- Roadmap: **sin puntos pendientes**. Changelog v1.7.0 añadido.

## 29. Hardening con tests (v1.7.1, 2026-11-18)

- 65 tests en total: 9 tests HTTP del GUI (`gui_test.go`, con `guiHandler()` + `httptest`), 10 tests
  unitarios/integración nuevos (`crawler_extra_test.go`), 30 del núcleo, 3 corpusfreqlib, 4 iolib,
  2 robotspolicy, 3 storelib, 2 stringlib, 2 redislib.
- `corpusfreqlib.Init()` devuelve `error` en vez de `os.Exit(1)` (necesario para testear en directorios
  temporales). `loadCorpus` también devuelve error.
- Test end-to-end: servidor HTTP local + perfil temporal + 2 workers → verifica corpus, frecuencias y
  contadores de caché/descarga.
- Bug detectado y corregido durante los tests: el POST de `/api/profile` exigía el nombre en la query
  y `/api/keywords/apply` duplicaba el separador `|`.
- Cobertura: main 68 %, iolib 74 %, robotspolicy 82 %, storelib 67 %. `go test -race ./...` ✅.

## 30. GUI: puerto ocupado (2026-11-18)

Si queda un proceso GUI anterior escuchando en 8765, el nuevo fallaba con `bind: address already in use`.
Se cerró el proceso colgante y se modificó `runGUI` para probar hasta 20 puertos consecutivos desde
8765, imprimiendo la URL real. Verificado: dos instancias simultáneas → 8765 y 8766.

## 31. Dashboard del Workbench (v1.7.3, 2026-11-18)

- `/api/stats` añade `running_count`, `not_accepted` y `rejections` (snapshot de `app.RejectionCounts`).
- `btn-stop` nace desactivado; `stats()` alterna `disabled` de start/stop según `running` y muestra
  el número de crawls activos y una tabla ordenada de razones de rechazo (con aviso de que los
  recuentos son por párrafo/enlace, no por página).
- Verificado con crawl real: visits=22, accepted=13, not_accepted=9, razones `stopword_ratio`,
  `too_short`, `non_english`, `low_relevancy`.

## 32. Workbench v1.7.4 (2026-11-18)

- Pestañas: Profiles (por defecto) → Crawler launcher → Keyword lab → Corpus search.
- `/api/last-profile`: deduce el último perfil ejecutado de `logs/<Profile>-YYYY-MM-DD-ZHH-MM-SS.txt`;
  el GUI lo selecciona al cargar.
- Keyword lab: stopwords inglesas ampliadas (~180 términos, incluye cannot/not/these/with); ranking
  por significación `count * log(1+docs) / log(2+freq_BNC)`; tabla con Word/Count/Docs/Score y botón
  para eliminar keywords antes de aplicarlas al perfil.

## 33. Workbench v1.7.5 (2026-11-18)

- Botón de eliminar keyword dentro de la celda de la palabra (un espacio entre palabra y botón).
- "Apply to profile" vuelve automáticamente a la pestaña Profiles y recarga el perfil.
- Agrupación morfológica con `snowballeng.Stem`: `image/images/imaging` → una sola keyword con
  lista de variantes (`details[].variants`).
- Quinta pestaña "Prompt generator" + `/api/prompt?profile=NAME`: prompt en inglés con instrucción
  exhaustiva, keywords, URLs semilla y extractos del texto curado del log.

## 34. Bugs del Workbench resueltos (v1.7.6, 2026-11-18)

- El launch de `skystacker-research` hacía panic en `scoreWithRegex` porque el regex del perfil no
  tiene grupo de captura y el código accedía a `k[1]`. Ahora se usa `k[1]` si existe, si no `k[0]`.
- `resolveProfileName` empareja perfiles ignorando mayúsculas y espacios, así que "Skystacker
  Research" resuelve a `skystacker-research.yaml`. Afecta a launch, profile view y prompt.
- `launchGUICrawl` ya no sustituye el puntero global `app`; usa `ResetForProfile` para evitar
  carreras con los handlers HTTP. Verificado: start → visits=50, accepted=16, sin error.
- Prompt generator: `<select id="pr-profile">` rellenado con los perfiles y el último ejecutado por defecto.

## 35. Fix JS del Workbench (v1.7.7, 2026-11-18)

- `loadProfile()` había perdido su `}` de cierre al editar el HTML incrustado → SyntaxError en todo
  el `<script>`, por lo que no funcionaban ni las pestañas ni los desplegables. Corregido y validado
  con `node --check`.
- Añadido `jsDelimitersBalanced` en `gui_test.go` (ignora strings y comprueba `{}`, `()`, `[]`) para
  detectar este tipo de rotura en CI, más tests de que `p-list` y `pr-profile` son `<select>` y de que
  todos los handlers JS están presentes.
- Verificado en checkout limpio: `go test ./...` ✅.

## 36. Keyword Lab: borrado real y extensión morfológica (v1.7.8, 2026-11-18)

- `removeKeyword` añade la palabra a `removed`; `applyKeywords` envía `{profile, keywords, removed}`.
- `/api/keywords/apply` resuelve el perfil (insensible a mayúsculas/espacios), combina keywords
  existentes + seleccionadas, elimina las marcadas (con su familia morfológica) y expande cada
  término con singular/plural/verbos (`image/imaged/images/imaging`, `stack/stacked/stacking/stacks`).
- Evita generar formas absurdas (`stackinger`, `drizzleion`). Verificado con curl y tests: perfil
  resultante solo contiene las variantes correctas.

## 37. Prompt completo y detalle de rechazos (v1.7.9, 2026-11-18)

- `/api/prompt` incluye los documentos curados completos (sin ` [...]`) y exige que todo el informe
  y todas las páginas aportadas estén en inglés.
- Nueva estructura `rejectionEvent` con URL, detalle, texto y timestamp; `recordRejection` guarda los
  10 últimos casos por razón (mutex propio). `/api/rejections?reason=X` los devuelve al GUI.
- El dashboard muestra las filas de razones como clicables; al pulsarlas se despliegan los últimos
  casos con enlaces `target="_blank"` y el motivo concreto (p. ej. "detected French ... expected
  English", "7 tokens < min 50", "score 4.2 < threshold 10").

## 38. Exclusiones persistentes y drill-down de rechazos (v1.8.0, 2026-11-18)

- Bug: las keywords eliminadas reaparecían porque solo se quitaban del corpus activo, no se
  persistía la exclusión. Ahora `/api/keywords/apply` escribe un bloque `excludedKeywords: |` en el
  perfil (con la familia morfológica) y `/api/keywords?profile=...` lo filtra en cada extracción.
- Bug: las filas de razones se generaban sin `onclick` (sustitución de string fallida). Restaurado
  `class=clickable onclick="showReason('reason')"`; verificado en HTML servido.
- Tests de integración: ciclo extraer → eliminar → aplicar → extraer de nuevo, y presencia del
  handler en las filas.

## 39. Caché persistente de robots.txt (v1.8.1, 2026-11-18)

- `robotspolicy.NewWithCache(userAgent, timeout, path)` guarda el cuerpo de cada `robots.txt` en
  `cache/robots-cache.json` (junto a `cache.db`), con `FetchedAt` y TTL de **7 días**. Al reiniciar se
  reconstruyen los grupos y el `Crawl-delay` sin volver a pedir el fichero. Escritura atómica
  (tmp + rename).
- Entradas caducadas se refrescan; fallos de red no se persisten.
- Tests: persistencia entre dos instancias (una sola petición HTTP) y expiración tras 8 días.
- Los ficheros `robots-cache.json` están en `.gitignore`.

## 40. Métricas de red y caché en el dashboard (v1.8.2, 2026-11-18)

- `networkRequestsTotal()` suma las peticiones HTTP de `proxyGet` + las de `robots.txt`
  (`robotspolicy.Requests()`); `storelib.Operations()` cuenta `Exists/Get/Set` en SQLite y Redis.
- `/api/stats` expone `network` y `cache_io`; el dashboard muestra "Net reqs" y "Cache I/O".
- Ambos contadores se ponen a cero en `ResetForProfile`. Verificado con crawl real: visits=22,
  cache=22, network=0 (todo desde caché), cache_io=47.

## 41. Propuestas GUI Studio al roadmap (v1.8.3, 2026-11-18)

- Analizadas 9 propuestas externas. Aclaración importante: el repo **no** usa React; el Workbench es
  un único string HTML/CSS/JS embebido en `crawler.go` y servido por `guiHandler()`, así que no hace
  falta mockup externo: se edita directamente y se cubre con `gui_test.go`.
- Todas aportan valor; añadidas como **P22–P30**, ordenadas de fácil a difícil: tema claro/oscuro,
  KPIs de velocidad/latencia/cola/goroutines, consola de eventos + exportación, Pause/Drain,
  configurador en vivo, SSE, registros de auditoría por URL, tabla filtrable y grafo/drawer.
- Recomendación: P22–P26 para la próxima release; P29–P30 para v2 (necesitan datos estructurados).

## 42. Segunda propuesta GUI al roadmap (v1.8.4, 2026-11-18)

- Analizada en `docs/GUI_MOCKUP_ANALYSIS.md`. Se descarta React/Vue (rompería el binario único sin
  dependencias); se mantienen las APIs `/api/*` y el HTML embebido.
- Añadidas ideas nuevas como P31–P47: design tokens, responsive, activity feed, gráfico de rechazos,
  sidebar, progress ring/ETA, editor estructurado con chips y validación, diff y versiones del
  perfil, Keyword Lab 2.0, Apply & Re-run, Corpus Explorer, heatmap de dominios, comparación de
  sesiones, command palette, export Markdown/PDF y análisis de impacto de keywords.
- Backlog completo P22–P47 ordenado por esfuerzo; priorizar P22–P36 antes de los ítems L/XL.

## 43. URL Seeds con contador (v1.8.5, 2026-11-18)

- Pestaña `hazURL` renombrada a **URL Seeds**; muestra "Seeds in profile: <b id=u-count>N</b> URLs".
- `loadURLs()` actualiza el contador con `d.urls.length` y `saveURLs()` muestra el total devuelto.
- Backend ya descarta duplicados y URLs inválidas; verificado con el perfil skystacker-research (53
  semillas, añadir 3 duplicados → sigue 53).

## 44. Roadmap S completado (v1.8.6, 2026-11-18)

- P22/P31/P33: design tokens con variables CSS, tema claro/oscuro persistente en localStorage,
  tipografía monoespaciada para URLs/métricas, spacing 4/8, radius consistente, micro-transiciones y
  media query para pantallas estrechas.
- P23: `/api/stats` añade `rate`, `avg_latency`, `queue`, `goroutines`; helpers `crawlRate`,
  `averageLatency`, `queueDepth` con `CrawlStartedAt`.
- P35: gráfico de barras de rechazos en CSS/SVG sin librerías (`#reject-chart`).
- P41: botón "Apply & Re-run" con `confirm()` que aplica keywords y lanza un crawl.
- Tests del HTML y de las métricas; `node --check` del JS embebido ✅.

## 45. Incidencia URLs Markdown y duplicados (v1.8.7, 2026-11-18)

- El usuario pegó 20 URLs en formato Markdown `[url](url)`; `parseURLs()` solo aceptaba líneas que
  empezaban por `http`, así que las descartó silenciosamente: por eso el crawl siguiente dio
  **0 net requests** (solo se visitaron semillas ya en caché). Las 20 URLs **no estaban en la caché**.
- Corregido: `reMarkdownLink` en Go y extracción de `(url)` en JS. Las 20 URLs se añadieron al perfil
  y la caché sigue limpia para ellas.
- Segundo problema: `replaceSeedBlock` solo reemplazaba el primer bloque de semillas, acumulando
  duplicados (105 líneas / 73 únicas). Reescrito para eliminar todas las líneas de semillas antes de
  escribir. Perfil limpio: 73 semillas únicas, todas las nuevas presentes.

## 46. Pestaña About (v1.8.8, 2026-11-18)

- Contenido habitual del About: nombre, versión, fecha, copyright, licencia, web/contacto, créditos, info del
  sistema y componentes (fuentes: CDE guidelines, GTK AboutDialog, GNOME HIG).
- Última pestaña "About" con `/api/about`: versión sincronizada (`appVersion`), copyright, MIT,
  autor/email, enlaces repo/issues/changelog/roadmap, versión de Go, plataforma, componentes y
  **QR local** del repositorio generado con `github.com/skip2/go-qrcode` (puro Go, sin servicio).

## 47. Cancelación, timeout y cola viva (v1.8.9, 2026-11-18)

- Señales globales en `run()`: primer Ctrl+C → `StopRequested` + `cancelCrawl()` + guardado de
  estado; segundo Ctrl+C → `os.Exit(130)`. `proxyGet` usa `http.NewRequestWithContext(crawlCtx,...)`.
- `maxRequestTimeout = 30s`: `downloadTimeout` se limita a ese valor al cargar `crawler.yaml`.
- `robotspolicy` cachea fallos (body vacío) durante 1 hora → no repite la petición para cada URL.
- Eventos de cola `requested/retrieved/failed` en un buffer de 30; `/api/queue` expone pendientes +
  recientes y el launcher los muestra con estados. Podado de enlaces terminados tras cada URL.
- Verificado: Ctrl+C termina limpiamente ("Interrupt received, saving state..." + "Done"), y la cola
  pasa de 15 pendientes a 0 mostrando transiciones requested→retrieved.

## 48. Ajustes finales del About (v1.8.10, 2026-11-18)

- Componentes separados con ", " en el JS; pestaña About en dos columnas (info izquierda, QR
  derecha) con apilado responsive.
- El nombre solo en el copyright; contacto = email + https://www.linkedin.com/in/semanticwebarchitect/.

## 49. KPIs en grid y 4 workers por defecto (v1.8.11, 2026-11-18)

- Diagnóstico del cuello de botella: con 1 worker, una petición lenta (hasta 30 s de timeout) bloquea
  toda la cola aunque haya cientos de URLs pendientes.
- Solución: `CLIWorkers` por defecto = 4 (también en CLI), selector en el launcher (1/2/4/8) que se
  envía en el POST `/api/crawl` como `workers`.
- Protección: las escrituras de `aLink.Status` y las lecturas de `prevState`/`finalStatus` usan
  `PoolMu`; `go test -race ./...` ✅.
- KPIs: grid CSS `repeat(auto-fit,minmax(118px,1fr))` con tarjetas etiqueta+valor, evitando el
  espacio vacío del flex-wrap anterior.
- Verificado en vivo: 4 workers, visits=22, cola 0 con transiciones requested→retrieved.

## 50. Stop y prompt generator robustos (v1.8.12, 2026-11-18)

- `/api/crawl/stop` llama a `cancelCrawl()`, marca `StopRequested` y `StateDirty`; `stats` expone
  `stopping` y el JS muestra "stopping..." hasta que el worker termina.
- `json()` del GUI lanza error visible; `generatePrompt()` captura errores y los muestra en el
  textarea. `/api/prompt` usa como fallback el último perfil ejecutado y luego cualquier perfil.
- `resolveProfileName` normaliza mayúsculas, espacios, guiones y guiones bajos.
- Tests de Stop, fallback de prompt y manejo de errores JS; `go test -race ./...` ✅.

## 28. Deadlock resuelto en isUniqueDocument (v1.6.1, 2026-11-18)

- Causa raíz: `isUniqueDocument()` tomaba `app.CrawlMu` con `defer Unlock` y dentro llamaba a
  `recordRejection()`, que vuelve a tomar `app.CrawlMu` → auto-deadlock. Los workers quedaban
  bloqueados en `recordRejection` (crawler.go:506) y el crawl se congelaba (~1486 líneas).
- Fix: leer/escribir `UniqueSignature` bajo lock y soltar el mutex antes de llamar a
  `recordRejection`. Test de regresión `TestIsUniqueDocumentDoesNotDeadlockOnDuplicate`.
- Verificación: crawl completo de `skystacker-research` termina con `crawl finished processed=126
  accepted=38` y `Done!!!`; `go test -race ./...` ✅.


## 29. Soporte PDF añadido al roadmap (v1.8.8, 2026-11-18)

- Análisis en `docs/PDF_SUPPORT_ANALYSIS.md`: mejor opción open source y pura Go es
  `github.com/ledongthuc/pdf` (BSD-3-Clause, fork de rsc/pdf); alternativa `dslipak/pdf`.
  Descartadas: pdfcpu (no extrae texto), go-fitz/Pdfium (cgo/WASM y AGPL) y unipdf (comercial).
- Roadmap: P48 (S) flags `process_pdf`/`pdf_max_bytes`/`pdf_pages_limit` + gates;
  P49 (M) descarga cruda con límite y validación `%PDF-`;
  P50 (M) wrapper `pdftextlib` (límite de páginas, fallos graceful);
  P51 (L) integración completa en pipeline/corpus + tests.
- OCR de PDFs escaneados queda fuera de alcance; se tratan como documentos demasiado cortos.


## 30. Revisión de bloqueos PDF en el código actual (v1.8.8, 2026-11-18)

- Tres puertas reales: (1) `isBanned()` aplica `regexLinkBannedTokens` global (`pdf`, `\.pdf`) antes
  de encolar; (2) `yamlInitSpecific()` no carga ese regex, por lo que el perfil no puede desactivarlo;
  (3) `download()` rechaza Content-Type no HTML **antes de leer el body**, así que no basta con quitar
  la extensión: hay que obtener bytes crudos y clasificar después. `collectlinks`/`html2text` también
  asumen HTML.
- Ajustado `docs/PDF_SUPPORT_ANALYSIS.md` (fetchBody previo al gate, flags en profile, prefijo de
  caché `pdf_`, razones `too_large`/`invalid_pdf`) y roadmap P48/P49.
