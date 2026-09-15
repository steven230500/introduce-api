package stats

import (
	"html/template"
	"time"

	"golang.org/x/text/language"
	"golang.org/x/text/language/display"
)

var months = [...]string{"ene", "feb", "mar", "abr", "may", "jun", "jul", "ago", "sep", "oct", "nov", "dic"}

// Bogotá, not UTC: the person reading this is in Colombia, and a service at
// 8 p.m. on Sunday should not be listed as Monday.
var readerZone = func() *time.Location {
	if loc, err := time.LoadLocation("America/Bogota"); err == nil {
		return loc
	}
	return time.UTC
}()

func shortDate(t time.Time) string {
	t = t.In(readerZone)
	return t.Format("2") + " " + months[t.Month()-1] + " " + t.Format("2006")
}

func countryName(code string) string {
	if code == "" {
		return "Sin identificar"
	}
	region, err := language.ParseRegion(code)
	if err != nil {
		return code
	}
	if name := display.Spanish.Regions().Name(region); name != "" {
		return name
	}
	return code
}

var dashboardPage = template.Must(template.New("stats").Funcs(template.FuncMap{
	"country": countryName,
	"date":    shortDate,
	"optDate": func(t *time.Time) string {
		if t == nil {
			return "Nunca"
		}
		return shortDate(*t)
	},
	"platform": func(p string) string {
		switch p {
		case "macos":
			return "Mac"
		case "windows":
			return "Windows"
		case "linux":
			return "Linux"
		case "":
			return "Sin identificar"
		}
		return p
	},
	// A breakdown table: its heading, how to name each label, and the rows.
	"rows": func(title, kind string, rows []Row) table {
		return table{Title: title, Kind: kind, Rows: rows}
	},
	"orBlank": func(s string) string {
		if s == "" {
			return "Sin identificar"
		}
		return s
	},
}).Parse(`<!doctype html>
<html lang="es">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="robots" content="noindex">
<title>Estadísticas: Introduce</title>
<style>
  :root { --tinta: #0e1838; --gris: #566079; --linea: #dde2eb; --fondo: #f1f3f8; }
  * { box-sizing: border-box; }
  body { margin: 0; background: var(--fondo); color: var(--tinta);
         font: 16px/1.5 -apple-system, "Segoe UI", Roboto, Arial, sans-serif; }
  main { max-width: 1080px; margin: 0 auto; padding: 32px 20px 64px; }
  h1 { margin: 0 0 4px; font-size: 30px; }
  h2 { margin: 40px 0 12px; font-size: 21px; }
  h3 { margin: 20px 0 8px; font-size: 16px; }
  .nota { margin: 0; color: var(--gris); font-size: 14px; }
  .cifras { display: flex; flex-wrap: wrap; gap: 12px; }
  .cifra { flex: 1 1 160px; background: #fff; border: 1px solid var(--linea); border-radius: 8px; padding: 14px 16px; }
  .cifra b { display: block; font-size: 30px; line-height: 1.1; font-variant-numeric: tabular-nums; }
  .cifra span { color: var(--gris); font-size: 14px; }
  .dos { display: grid; grid-template-columns: repeat(auto-fit, minmax(300px, 1fr)); gap: 0 24px; }
  .tabla { overflow-x: auto; background: #fff; border: 1px solid var(--linea); border-radius: 8px; }
  table { width: 100%; border-collapse: collapse; font-size: 14px; }
  th, td { padding: 8px 12px; text-align: left; border-bottom: 1px solid var(--linea); white-space: nowrap; }
  th { color: var(--gris); font-weight: 600; }
  td.n, th.n { text-align: right; font-variant-numeric: tabular-nums; }
  tr:last-child td { border-bottom: 0; }
  .vacio { padding: 12px; color: var(--gris); font-size: 14px; }
  footer { margin-top: 40px; color: var(--gris); font-size: 13px; }
</style>
</head>
<body>
<main>
  <h1>Estadísticas de Introduce</h1>
  <p class="nota">Descargas desde el sitio y copias de la app abiertas. Sin nombres ni direcciones IP, salvo lo que cada iglesia dio al registrarse.</p>

  <h2>Descargas desde el sitio</h2>
  <div class="cifras">
    <div class="cifra"><b>{{.Downloads.Week}}</b><span>últimos 7 días</span></div>
    <div class="cifra"><b>{{.Downloads.Month}}</b><span>últimos 30 días</span></div>
    <div class="cifra"><b>{{.Downloads.Total}}</b><span>en total</span></div>
  </div>
  <div class="dos">
    <div>
      <h3>Por sistema</h3>
      {{template "tabla" (rows "Sistema" "platform" .DownloadsByPlatform)}}
    </div>
    <div>
      <h3>Por país</h3>
      {{template "tabla" (rows "País" "country" .DownloadsByCountry)}}
    </div>
  </div>

  <h2>Copias de la app en uso</h2>
  <p class="nota">Computadoras distintas que abrieron la app. Solo cuentan las de la versión 1.0.2 en adelante.</p>
  <div class="cifras">
    <div class="cifra"><b>{{.Copies.Week}}</b><span>abiertas en 7 días</span></div>
    <div class="cifra"><b>{{.Copies.Month}}</b><span>abiertas en 30 días</span></div>
    <div class="cifra"><b>{{.Copies.Total}}</b><span>alguna vez</span></div>
  </div>
  <div class="dos">
    <div>
      <h3>Por país</h3>
      {{template "tabla" (rows "País" "country" .CopiesByCountry)}}
    </div>
    <div>
      <h3>Por versión</h3>
      {{template "tabla" (rows "Versión" "text" .CopiesByVersion)}}
      <h3>Por sistema</h3>
      {{template "tabla" (rows "Sistema" "platform" .CopiesByPlatform)}}
    </div>
  </div>

  <h2>Iglesias</h2>
  <div class="cifras">
    <div class="cifra"><b>{{len .Churches}}</b><span>iglesias registradas</span></div>
    <div class="cifra"><b>{{.Accounts}}</b><span>cuentas</span></div>
    <div class="cifra"><b>{{.AccountsWithoutChurch}}</b><span>cuentas sin iglesia todavía</span></div>
  </div>
  <div class="tabla" style="margin-top: 12px">
  {{if .Churches}}
    <table>
      <tr><th>Iglesia</th><th>País</th><th>Registrada</th><th class="n">Miembros</th><th class="n">Pendientes</th><th>Administradores</th><th>Último uso en pantalla</th><th class="n">Días con servicio (30 días)</th><th>App abierta</th></tr>
      {{range .Churches}}
      <tr><td>{{.Name}}</td><td>{{country .Country}}</td><td>{{date .CreatedAt}}</td><td class="n">{{.Members}}</td><td class="n">{{.Pending}}</td><td>{{.Admins}}</td><td>{{optDate .LastProjection}}</td><td class="n">{{.DaysProjected}}</td><td>{{optDate .LastOpened}}</td></tr>
      {{end}}
    </table>
  {{else}}<p class="vacio">Todavía no hay iglesias.</p>{{end}}
  </div>

  <footer>Países según <a href="https://db-ip.com">IP Geolocation by DB-IP</a>, licencia CC BY 4.0.</footer>
</main>
</body>
</html>
{{define "tabla"}}<div class="tabla">{{if .Rows}}
  <table>
    <tr><th>{{.Title}}</th><th class="n">7 días</th><th class="n">30 días</th><th class="n">Total</th></tr>
    {{$kind := .Kind}}{{range .Rows}}
    <tr><td>{{if eq $kind "country"}}{{country .Label}}{{else if eq $kind "platform"}}{{platform .Label}}{{else}}{{orBlank .Label}}{{end}}</td><td class="n">{{.Week}}</td><td class="n">{{.Month}}</td><td class="n">{{.Total}}</td></tr>
    {{end}}
  </table>{{else}}<p class="vacio">Sin datos todavía.</p>{{end}}</div>{{end}}
`))

type table struct {
	Title string
	Kind  string
	Rows  []Row
}
