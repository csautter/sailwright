// Calls one method of the local Orca runtime RPC through the CLI's own client
// and prints the JSON envelope. Used for the SSH target methods (ssh.addTarget,
// ssh.connect, ...) that the `orca` CLI does not expose as commands.
//
//   ELECTRON_RUN_AS_NODE=1 <Orca.app>/Contents/MacOS/Orca orca-rpc.js <cli-dir> <method> [params-json]
//
// Undocumented interface: re-check after Orca updates (scripts/orca-vm/docker-workspace.sh doctor).
'use strict'

const [cliDir, method, paramsJson] = process.argv.slice(2)
if (!cliDir || !method) {
  console.error('usage: orca-rpc.js <orca-cli-dir> <method> [params-json]')
  process.exit(2)
}

const { RuntimeClient } = require(`${cliDir}/runtime-client.js`)

;(async () => {
  const client = new RuntimeClient()
  const params = paramsJson ? JSON.parse(paramsJson) : undefined
  const response = await client.call(method, params)
  process.stdout.write(`${JSON.stringify(response)}\n`)
  if (response && response.ok === false) process.exit(1)
})().catch((error) => {
  console.error(`orca-rpc ${method}: ${error && error.message ? error.message : error}`)
  process.exit(1)
})
