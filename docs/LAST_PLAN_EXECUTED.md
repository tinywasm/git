---
PLAN: "fix: un fallo de gh dejaba de borrar el token de GitHub ya validado"
EXECUTOR: jules
REVIEWER: none
---

> Plan autocontenido: todo lo necesario para ejecutarlo está aquí.
> Se despacha con el flujo CodeJob. Ver skill: agents-workflow.

# Plan — dejar de destruir el token bueno de GitHub

## 1. El síntoma que reporta el usuario

`tinywasm -tui` pide el device flow de GitHub **una y otra vez**, aunque ya se
haya completado:

```
22:09:01   GitHub Auth   Paste this code in browser: 3F81-CD63
22:12:53   GitHub Auth   GitHub authentication successful!
   … y en el siguiente arranque, otra vez el mismo código nuevo
```

## 2. Lo que NO es (comprobado, no lo investigues de nuevo)

- **El token sí se guarda.** Leído del llavero real del sistema: servicio
  `devflow`, clave `github_token`, 40 caracteres presentes.
- **El llavero del sistema funciona.** `gnome-keyring-daemon` corre con el
  componente `secrets` y `org.freedesktop.secrets` responde en el bus de sesión.
- **`gh` está instalado y autenticado**: `gh auth status` → exit 0.
- **`EnsureGitHubAuth` devuelve `nil` sin device flow** cuando todo está en su
  sitio. El camino feliz funciona.

El fallo está en el camino **infeliz**: qué hace cuando la validación tropieza.

## 3. La causa

`github_auth.go:84-110`:

```go
func (a *GitHubOAuth) EnsureGitHubAuth() error {
	if a.store == nil { ... }

	token, err := a.store.Get(githubTokenKey)
	if err == nil && token != "" {
		if a.configureGhWithToken(token) == nil {              // ← 1
			if _, err := command.Run("gh", "auth", "status"); err == nil {
				return nil                                     // ← 2
			}
		}
		a.store.Delete(githubTokenKey)                          // ← 3  EL DEFECTO
	}

	token, err = a.DeviceFlowAuth()
	...
}
```

Tres problemas, en orden de gravedad:

**3.1 · Se borra la credencial por un fallo ajeno (línea 3).**
`configureGhWithToken` ejecuta `gh auth login --with-token` y
`command.Run("gh","auth","status")` ejecuta otro binario externo. Cualquiera de
los dos falla por motivos que **no dicen nada sobre la validez del token**:

- el llavero de `gh` todavía bloqueado justo después de iniciar sesión,
- `gh` aún no está en el `PATH` del proceso (demonio arrancado temprano),
- otro proceso escribiendo la configuración de `gh` a la vez,
- sin red.

En todos esos casos se borra un token perfectamente válido y se obliga al
usuario a repetir el device flow.

**3.2 · Se reescribe la sesión de `gh` en cada arranque (línea 1).**
`gh auth login --with-token` se ejecuta **siempre**, incluso cuando `gh` ya
tiene exactamente ese mismo token. Es una mutación de la configuración del
usuario que nadie pidió, y una oportunidad de fallo por cada arranque.

**3.3 · Es mudo.** No hay ni una línea de log que diga *por qué* se volvió a
autenticar. El usuario ve el device flow y no tiene forma de saber qué pasó.
Esto viola el principio 6 del `CONSTRUCTION_HARNESS` (error de compilación →
diagnóstico ruidoso → nunca fallo silencioso).

**Agravante fuera de este repo:** `tinywasm/app` reinicia el demonio en cada
cambio de proyecto (`app/net.go`, `shouldRestartDaemon`), y cada reinicio vuelve
a pasar por aquí. No se arregla aquí, pero explica la frecuencia.

## 4. El cambio

### 4.1 · Separar "token inválido" de "la herramienta falló"

Un token solo es inválido si **GitHub** lo dice. Añadir a `github_auth.go`:

```go
// tokenValidationResult distingue los tres desenlaces posibles. Sin esta
// distinción cualquier tropiezo de una herramienta externa se interpretaba
// como "credencial inválida" y borraba el token.
type tokenValidationResult uint8

const (
	tokenValid           tokenValidationResult = iota // GitHub aceptó el token
	tokenRejected                                     // GitHub devolvió 401: el token ya no sirve
	tokenUnverifiable                                 // no se pudo comprobar: red, gh, llavero…
)
```

Y la validación contra la API, sin depender de `gh`:

```go
const githubAPIUserURL = "https://api.github.com/user"

// validateToken pregunta a GitHub directamente. Es la ÚNICA autoridad sobre si
// un token sirve: gh puede fallar por su propia configuración sin que el token
// tenga nada que ver.
func (a *GitHubOAuth) validateToken(token string) tokenValidationResult
```

- HTTP 200 → `tokenValid`
- HTTP 401 → `tokenRejected`
- cualquier otra cosa (error de transporte, 5xx, timeout) → `tokenUnverifiable`

Usa un `http.Client` con timeout explícito (`10 * time.Second`) declarado como
constante con nombre. No metas el token en la URL ni en argumentos de proceso:
va en la cabecera `Authorization: Bearer <token>`.

### 4.2 · Borrar solo cuando GitHub rechaza

```go
switch a.validateToken(token) {
case tokenValid:
	if err := a.ensureGhSessionMatches(token); err != nil {
		a.log("aviso: el token es válido pero no se pudo configurar gh:", err)
	}
	return nil

case tokenRejected:
	a.log("GitHub rechazó el token guardado (401): hay que autenticarse de nuevo")
	a.store.Delete(githubTokenKey)
	// sigue al device flow

case tokenUnverifiable:
	// NO se borra nada. El token puede ser perfectamente bueno.
	return fmt.Errorf("no se pudo verificar el token de GitHub (¿sin red?): se conserva el guardado; reintenta o borra con …")
}
```

Decisión de diseño a respetar: en `tokenUnverifiable` **no** se lanza el device
flow. Pedirle al usuario que se autentique porque no había red es exactamente el
bucle del que sale este plan.

### 4.3 · No reescribir la sesión de `gh` si ya coincide

```go
// ensureGhSessionMatches configura gh SOLO si su token activo no es ya éste.
// gh auth login --with-token reescribe la configuración del usuario; hacerlo en
// cada arranque es una mutación gratuita y una oportunidad de fallo por arranque.
func (a *GitHubOAuth) ensureGhSessionMatches(token string) error {
	current, err := command.Run("gh", "auth", "token")
	if err == nil && strings.TrimSpace(current) == token {
		return nil
	}
	return a.configureGhWithToken(token)
}
```

`configureGhWithToken` se queda como está (`github_auth.go:268`); solo cambia
**quién** y **cuándo** la llama.

### 4.4 · Que se oiga

Cada rama registra su motivo con `a.log(...)`. Mensajes exactos, como constantes
con nombre — nada de literales sueltos en la lógica.

> Nota de secuencia: hasta que se publique el arreglo de
> `tinywasm/fmt` (`Sprint(error)` devuelve `""`), un `a.log("texto:", err)`
> imprime el prefijo y **pierde** el error. Mientras tanto, registra el texto ya
> compuesto: `a.log("motivo: " + err.Error())`.

## 5. Lo que NO se toca

- **`DeviceFlowAuth`** (`github_auth.go:112-150`): funciona. No cambia.
- **`GitHubPATAuth`** (`github_pat_auth.go`): es el otro autenticador, con la
  clave `GH_TOKEN`. Fuera de alcance.
- **La constante `githubTokenKey = "github_token"`** y el servicio `devflow`:
  **no los renombres bajo ningún concepto.** Hay tokens guardados ahí; cambiar
  el nombre es equivalente a borrarlos todos.
- **`SecretStore`** (`secret_store.go`): el contrato está bien; sigue admitiendo
  `nil`.

## 6. Tests

En `tests/`, con un `SecretStore` de mentira (mapa en memoria) y un validador
inyectable — **no** llames a la API de GitHub de verdad en un test.

Para poder inyectarlo, `validateToken` debe ser un campo/función sustituible en
el struct, no una llamada directa incrustada:

```go
type GitHubOAuth struct {
	...
	validate func(token string) tokenValidationResult // nil ⇒ la implementación real
}
```

| Test | Qué prueba |
|---|---|
| `TestTokenValidoNoLanzaDeviceFlow` | `tokenValid` → devuelve `nil` y el token sigue en el store |
| `TestGitHubRechazaElTokenSeBorraYSeReautentica` | `tokenRejected` → el token se borra |
| `TestSinRedElTokenSobrevive` | `tokenUnverifiable` → **el token sigue en el store** y devuelve error |
| `TestNoSeReconfiguraGhSiYaTieneElMismoToken` | `ensureGhSessionMatches` no ejecuta `gh auth login` |
| `TestCadaRamaRegistraSuMotivo` | el log contiene el motivo (contra el fallo silencioso) |

El tercero es el test que define este plan: **es el que hoy falla.**

## 7. Criterios de aceptación

| # | Comprobación | Esperado |
|---|---|---|
| 1 | `go test ./...` | verde |
| 2 | `grep -n "store.Delete(githubTokenKey)" github_auth.go` | **una sola** aparición, dentro de la rama `tokenRejected` |
| 3 | `grep -rn "gh\", \"auth\", \"status\"" .` | ya no decide si se borra el token |
| 4 | `grep -n "githubTokenKey = " github_auth.go` | sigue siendo `"github_token"` |
| 5 | Arrancar sin red con un token guardado | no pide device flow, avisa y conserva el token |

## 8. Etapas

| # | Etapa | Archivos |
|---|---|---|
| 1 | `tokenValidationResult` + `validateToken` contra la API | `github_auth.go` |
| 2 | Reescribir el flujo de `EnsureGitHubAuth` con el `switch` | `github_auth.go` |
| 3 | `ensureGhSessionMatches` | `github_auth.go` |
| 4 | Mensajes de log como constantes | `github_auth.go` |
| 5 | Tests con store y validador inyectados | `tests/` |
