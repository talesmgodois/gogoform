# 000021 open-source-preparation

# Open source preparation
## TODOS

- The readm of the project, add a contributing section
- MIT licence documentation
- Update the deploy.yml to deploy the image on dockerhub if there are taghs and using github registry if it uses tags, but do that using variables, REGISTRY_RELEASES, REGISTRY_MAIN_BRANCH, 
- Add another docker compose to use the registry releases
- Implement on go a hot/live reload for dev 
- Using github actions, generate executables for linux and windows, and make them available in the releases sections of github

<!-- RUNNER:PLAN -->
## Plan

1. [x] Add an MIT `LICENSE` file at the repo root.
2. [x] Create `README.md` with project overview, usage/setup instructions, and a Contributing section; link to `LICENSE`.
3. [x] Add `.github/workflows/deploy.yml` to build and push the Docker image — to GitHub Container Registry on main-branch pushes, to Docker Hub on tag pushes — driven by repo variables `REGISTRY_MAIN_BRANCH` and `REGISTRY_RELEASES`.
4. [x] Add a second docker-compose file (e.g. `docker-compose.release.yml`) that runs the app from the `REGISTRY_RELEASES` image instead of building locally, alongside the existing postgres/adminer services.
5. [x] Implement Go hot/live reload for local dev (e.g. via `air`), adding its config and a `make watch` target, documented in the README.
6. [x] Add `.github/workflows/release-binaries.yml` to cross-compile linux and windows binaries on tag push and attach them as GitHub Release assets.
