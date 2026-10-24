# Deploying Payahub

Two compose files:

| File | For | What runs |
| --- | --- | --- |
| `docker-compose.yml` | Your machine | Postgres, `payahub dev` (API and a worker in one process, sandbox key `sk_sandbox_dev`), the web app, Mailpit |
| `docker-compose.prod.yml` | Dokploy (or any Docker host) | Postgres, a `migrate` step, the API, workers, the web app |

## Local

```bash
docker compose up --build
```

| Address | What |
| --- | --- |
| http://localhost:3000 | Web app: landing page, sign-up, console |
| http://localhost:8080 | API (`Authorization: Bearer sk_sandbox_dev`) |
| http://localhost:8025 | Mailpit: every email Payahub sends, invitation links included |
| localhost:5432 | Postgres, user `postgres`, password `payahub` |

Data stays in the `pgdata` volume between runs; `docker compose down -v` starts afresh.
The local stack serves plain http, so it sets `PAYAHUB_INSECURE_COOKIES=1` to drop the
Secure flag from session cookies. Production never sets it.

## Production on Dokploy

### What runs

```
            Traefik (Dokploy, HTTPS)
             |                   |
   WEB_DOMAIN:3000        API_DOMAIN:8080
             |                   |
            web ----private---> api        worker x N
                                 \           /
                                  postgres (volume pgdata)
   migrate: runs once per deploy, before api and worker start
```

- **migrate** applies the embedded migrations and exits. They are idempotent, so it runs on
  every deploy; api and worker wait for it to finish successfully (`service_completed_successfully`),
  so new code never starts on an old schema.
- **api** serves `/v1`, including real-time submission (`Prefer: wait=N`). Docker checks it every 10s
  with `payahub healthcheck`, which calls `/v1/health` (the database is pinged too). Traefik only
  routes to it once healthy.
- **worker** works the durable queue: submissions, follow-ups, email, webhooks. Scale with
  `PAYAHUB_WORKER_REPLICAS` (containers) and `PAYAHUB_WORKERS` (concurrent submissions in each).
  A stopped worker finishes in-flight submissions within 30s (Docker waits 45s); an unfinished job
  is retried by another worker, which asks the payer whether it already has the claim first.
- **web** is the Next.js server. It talks to the API over the private network
  (`http://payahub-api:8080`); the browser never gets a token.
- **postgres** keeps its data in the named volume `pgdata`.

### First deploy

1. **DNS.** Point A records for the web and API hostnames (for example `payahub.com` and
   `api.payahub.com`) at the Dokploy server.
2. **Create the app.** In a Dokploy project: Create Service, Compose, type *Docker Compose* (not
   Stack: Stack mode cannot build images). Provider: GitHub, repository `harrisyn/payahub`, branch
   `main`, compose path `./docker-compose.prod.yml`.
3. **Environment.** Paste `.env.example` into the Environment tab and fill it in:
   - `POSTGRES_PASSWORD`: `openssl rand -hex 24` (hex, so it is safe inside the database URL).
   - `PAYAHUB_VAULT_KEY`: `openssl rand -base64 32`. **Keep a copy in your password manager.** It
     seals every organisation's insurer credentials and the model keys; without it they must all be
     entered again.
   - `WEB_DOMAIN`, `API_DOMAIN`: the hostnames from step 1, without `https://`.
   - Email and Paystack when you have them (see the tracker).
   The deploy refuses to start if a required one is missing, and says which.
4. **Domains.** In the Domains tab add two:
   - host = `WEB_DOMAIN`, service `web`, port `3000`, path `/`, HTTPS on, certificate Let's Encrypt
   - host = `API_DOMAIN`, service `api`, port `8080`, path `/`, HTTPS on, certificate Let's Encrypt
   Dokploy adds the Traefik labels and joins those two services to its network at deploy time
   (Preview Compose shows the result).
5. **Isolated Deployments** (Advanced): turn it on. It keeps this app's containers on their own
   network. The compose file is safe without it too: services reach each other by the aliases
   `payahub-postgres` and `payahub-api`, so another app's `api` or `postgres` on the shared
   `dokploy-network` is never picked up.
6. **Deploy.** Watch the logs: `migrate` exits 0, `api` turns healthy, then `web`.
7. **First operator.** In the app's terminal (any backend container, e.g. `api`), after you have
   signed up on the site:
   ```bash
   /payahub operator you@yourdomain.com
   ```
8. **Check.** `curl https://API_DOMAIN/v1/health` gives `{"status":"ok"}`; sign up on
   `https://WEB_DOMAIN/start` and send a sandbox claim from the console.

Turn on Auto Deploy (GitHub webhook) if every merge to `main` should go out.

### Backups

Set up a Volume Backup in Dokploy for `pgdata` to S3-compatible storage (Volume Backups work only
with named volumes, which is why the database uses one). For point-in-time recovery or a managed
database instead, create a Dokploy Postgres service (or any Postgres 16), set `DATABASE_URL` to it
with `sslmode` as appropriate, and remove the `postgres` service from the compose file (it would
otherwise still start, unused).

Store the vault key apart from the database backups: a backup is no use without it, and the two
together open every credential.

### Scaling

- More submissions in flight: raise `PAYAHUB_WORKERS` (default 100 per worker).
- More worker containers: raise `PAYAHUB_WORKER_REPLICAS`. Jobs are taken from Postgres with
  row locks, so any number of workers can share the queue safely.
- Every Payahub process keeps a pool of up to 8 connections per CPU. Keep
  `POSTGRES_MAX_CONNECTIONS` above (api + workers + 1) x 8 x CPUs. Raise `POSTGRES_SHARED_BUFFERS`
  to about a quarter of the server's memory.
- The measured limit on one 4-core machine is the disk's commit flush (about 480 claims a second
  end to end, see the tracker), so a fast disk for the `pgdata` volume matters more than CPU.

### Releases and rollback

Each deploy builds the images from the commit. Set `PAYAHUB_RELEASE` to the commit or a version to
tag them; to roll back, redeploy the earlier commit from Dokploy's Deployments list. Rollback
works because migrations are kept additive: older code runs on a newer schema. A migration that
removes a column or table ships in a later release than the code that stops using it.

### If a deploy fails

- `migrate` exited non-zero: its logs say which migration. The new api and worker are not
  started. Fix forward and redeploy; migrations are idempotent, so a rerun picks up where it stopped.
- `api` unhealthy: `/v1/health` reports `degraded` when the database does not answer. Check
  `DATABASE_URL` and that `postgres` is healthy.
- The web app loads but sign-in or the console fails: it cannot reach `http://payahub-api:8080`;
  check the api container is healthy (web waits for it at start, but not after).
- Required variable missing: the deploy log names it.
