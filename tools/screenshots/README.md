# Screenshot capture

Regenerates the README/launch screenshots in [`docs/images/`](../../docs/images)
from the **released** images (never a local build — screenshots must show what
users get). Re-run this after any release that changes the UI.

## Recipe

From the repo root:

```bash
# 1. Start the release sandbox on collision-free ports, isolated from any
#    other compose project on the machine (never omit -p).
AVURUOBS_VERSION=v0.8.0 docker compose -p avuru-shots \
  -f deploy/compose/docker-compose.release.yaml \
  -f tools/screenshots/compose.screenshots.yaml up --wait

# 2. Seed the deterministic fixtures (health tiers, alert rules, green energy).
(cd tools/seed && go run . -endpoint http://localhost:14318 \
  -fixtures ../../deploy/compose/seed/fixtures)

# 3. Warm HotROD + capture (writes docs/images/*.png).
(cd ui && npx playwright test -c playwright.screenshots.config.ts)

# 4. Tear down ONLY the isolated project.
docker compose -p avuru-shots down -v
```

## Notes

- Pin `AVURUOBS_VERSION` to the exact released tag being shown.
- The capture warms HotROD for ~90s first so the service map and trace
  waterfall carry realistic multi-service traffic; `/errors`, `/health`,
  `/alerts` and `/green` render from the seeded fixtures and are deterministic.
- The green shot uses `?range=24h` — at narrow ranges the seeded counter pair
  never co-buckets and the page shows its empty state (see the header comment
  in `ui/e2e/green.spec.ts`). If the green page comes up empty, re-run the
  seeder: that is the documented ~2% bucket-boundary flake, not a bug.
- Images are captured at 1600×1000 CSS px, 2× density (3200×2000 actual). If a
  file exceeds ~600 KB, downscale it: `sips --resampleWidth 1600 <file>`.
