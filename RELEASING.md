# Releasing sprite-tunnel

Releases are built and published by `.github/workflows/release.yml` when a version tag is pushed. No personal access token or release secret is needed: the workflow uses GitHub's scoped token and `gh`.

1. Add a `## vX.Y.Z` section to `CHANGELOG.md` describing the final changes and limitations.
2. Run `make check` and `python3 scripts/release.py --version vX.Y.Z` locally. Test `dist/smoke/sprite-tunnel --version` and inspect `dist/checksums.txt` and `dist/release-notes.md`.
3. Commit the changes, push `main`, and wait for CI:
   ```sh
   gh run list --workflow ci.yml --branch main
   gh run watch RUN_ID --exit-status
   ```
4. Tag that exact passing commit and push the tag:
   ```sh
   git tag -a vX.Y.Z -m 'Release vX.Y.Z'
   git push origin vX.Y.Z
   ```
5. Watch the release workflow with `gh run watch RUN_ID --exit-status`, then verify the six archives and checksum file with `gh release view vX.Y.Z`.

The release workflow reruns CI before building. Release versions and commit/date metadata are embedded in the executable. The build script accepts semantic-version tags, including prerelease suffixes; prereleases are marked accordingly on GitHub.

Never move or overwrite a published tag. If a published release needs a fix, use a new patch version. If an unpublished release workflow fails, fix the cause and rerun failed jobs when possible. Before manually recovering a partially created release, inspect its existing assets rather than overwriting them blindly.

Ordinary CI uses in-process tests and does not create Sprites. If live verification is needed, use only `https://api.sprites.dev`, label the newly created Sprite `app:sprite-tunnel`, and delete it after testing. Do not include credentials in release artifacts.
