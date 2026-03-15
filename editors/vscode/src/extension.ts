import * as vscode from "vscode";
import {
  LanguageClient,
  LanguageClientOptions,
  ServerOptions,
} from "vscode-languageclient/node";

let client: LanguageClient | undefined;

export function activate(context: vscode.ExtensionContext) {
  startClient();

  context.subscriptions.push(
    vscode.workspace.onDidChangeConfiguration((e) => {
      if (e.affectsConfiguration("doit.binaryPath")) {
        stopClient().then(() => startClient());
      }
    })
  );
}

export function deactivate(): Promise<void> | undefined {
  return stopClient();
}

function getBinaryPath(): string {
  const configured = vscode.workspace
    .getConfiguration("doit")
    .get<string>("binaryPath", "");
  return configured || "doit";
}

function startClient() {
  const binary = getBinaryPath();

  const serverOptions: ServerOptions = {
    command: binary,
    args: ["language-server"],
  };

  const clientOptions: LanguageClientOptions = {
    documentSelector: [{ scheme: "file", language: "doit" }],
  };

  client = new LanguageClient(
    "doit",
    "doit Language Server",
    serverOptions,
    clientOptions
  );

  client.start().catch((err) => {
    client = undefined;
    if (binary === "doit") {
      // No explicit path and not on PATH — silent fallback to TextMate only
      return;
    }
    vscode.window.showErrorMessage(
      `Failed to start doit language server: ${err.message}`
    );
  });
}

function stopClient(): Promise<void> {
  if (!client) {
    return Promise.resolve();
  }
  const c = client;
  client = undefined;
  return c.stop();
}
