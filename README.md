# portlift

Put the server running on your machine on a public URL, with one command.

```
$ portlift
node · 5173 · C:\code\myapp
https://random-words.trycloudflare.com
c: copy URL · r: QR code · Ctrl+C: stop
```

Open the URL on your phone, or send it to someone. It works from any network
and stops when you press Ctrl+C. There is no account, no config file and
nothing left running afterwards.

portlift is a thin wrapper around a [Cloudflare quick tunnel][quick]. What it
adds is finding the right server for you, getting `cloudflared` if you don't
have it, and telling you when your dev server is going to reject the tunnel.

## Install

From a clone of this repo:

```
go install .
```

## Usage

```
portlift            expose the server started from this folder
portlift <port>     expose a specific port
portlift <url>      expose a specific local URL

--qr                show a QR code for the URL on startup
-q, --quiet         print only the URL
```

While the tunnel is up, press `c` to copy the URL to the clipboard, or `r` to
show a QR code for it.

With `-q`, or when the output is piped, portlift prints the URL and nothing
else, and never reads the keyboard or asks a question:

```
portlift 3000 -q > url.txt &
```

## How it finds your server

Run with no arguments, portlift looks for an HTTP server whose process was
started from where you are. It never looks at the rest of the machine.

| Where the server was started               | What portlift does         |
| ------------------------------------------ | -------------------------- |
| This folder, or a folder below it          | Uses it                    |
| A folder above this one, up to the repo root | Uses it                  |
| Somewhere else in the same repo            | Shows it and asks first    |
| Anywhere else                              | Ignores it                 |

Closer wins: a server below you beats one above you, which beats one elsewhere
in the repo. If two servers are equally close, portlift lists them and asks.

The repo root is the nearest folder above you with a `.git` in it. Outside a
git repo there is no root to look up to, so only this folder and the ones below
it are checked.

Servers running inside WSL or Docker are not found, because Windows reports
them as belonging to a relay process with no project folder. Use
`portlift <port>` for those.

## cloudflared

portlift uses the `cloudflared` on your `PATH`. If there isn't one, it offers
to download a pinned release from Cloudflare's GitHub releases into your user
cache folder, and checks the download against the published SHA-256 before
using it.

## Things to know

- **The URL is public.** Anyone who has it can reach your server for as long as
  portlift is running.
- **Quick tunnels are for development.** Cloudflare limits them to 200
  concurrent requests, gives no uptime guarantee, and does not support
  Server-Sent Events, so a streaming endpoint may hang through the tunnel.
- **Some dev servers block unknown hosts.** portlift checks for this on startup
  and prints the setting to change for Vite, webpack-dev-server, Rails and
  Django. Next.js serves the page but refuses hot reload; portlift checks for
  that too and points you at `allowedDevOrigins`.
- **An existing `~/.cloudflared/config.yml` gets in the way.** Cloudflare does
  not support quick tunnels while one exists; portlift warns you if it finds
  one.

[quick]: https://developers.cloudflare.com/cloudflare-one/connections/connect-networks/do-more-with-tunnels/trycloudflare/
