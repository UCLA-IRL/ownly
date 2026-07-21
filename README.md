<img height=90 src="./src/assets/logo.svg" alt="Ownly" />

Secure decentralized workspace built over the [Named Data Networking](https://named-data.net) stack.

[![ci](https://github.com/pulsejet/ownly/actions/workflows/ci.yml/badge.svg)](https://github.com/pulsejet/ownly/actions/workflows/ci.yml)
[![license](https://img.shields.io/badge/license-MIT-blue)](./LICENSE)
[![Website](https://img.shields.io/website?url=https%3A%2F%2Fownly.work)](https://ownly.work)

## Development Setup

It is recommended to use [VSCode](https://code.visualstudio.com/) with the following extensions:

- [Volar](https://marketplace.visualstudio.com/items?itemName=Vue.volar) for Vue 3 support.
- [ESLint](https://marketplace.visualstudio.com/items?itemName=dbaeumer.vscode-eslint) for TypeScript linting.
- [Prettier](https://marketplace.visualstudio.com/items?itemName=esbenp.prettier-vscode) for code formatting.
- [Go](https://marketplace.visualstudio.com/items?itemName=golang.Go) for Go language support.

For debugging, install the [NDN-Play Devtools](https://chromewebstore.google.com/detail/ndn-play-devtools/iknhkednlmhmcooifnplndiahiopfmnh?hl=en) and [OPFS Viewer](https://chromewebstore.google.com/detail/opfs-viewer/bebjgdnmkhibekhoijhhbdpfdddpefci) extensions.

To build the WebAssembly module, install [Go 1.23](https://go.dev/doc/install)

```sh
npm install     # install dependencies

npm run dev     # vite dev server (HMR)
npm run build   # vite prod build
npm run lint    # eslint

npm run go:wasm # build Go WebAssembly module
```

The latest deployment uses Node version 22.
