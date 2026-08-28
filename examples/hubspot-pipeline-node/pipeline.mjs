import http from "node:http";
import https from "node:https";
import tls from "node:tls";
import {URL} from "node:url";

const USAGE = `hubspot-pipeline: run this app under Fabricate.

  fab run acme-hubspot -- node pipeline.mjs
  fab run acme-hubspot --proxy -- node pipeline.mjs
`;

function env(...names) {
  for (const name of names) {
    const value = process.env[name];
    if (value) return value;
  }
  return undefined;
}

function usingProxy() {
  return Boolean(env("HTTPS_PROXY", "https_proxy"));
}

function hubspotBase() {
  if (usingProxy()) return {base: "https://api.hubapi.com", token: undefined};
  const url = env("FAB_HUBSPOT_URL", "FAB_CRM_URL");
  const token = env("FAB_HUBSPOT_TOKEN", "FAB_CRM_TOKEN");
  if (!url || !token) {
    console.error(USAGE);
    process.exit(1);
  }
  return {base: url.replace(/\/$/, ""), token};
}

function headerLines(headers) {
  return Object.entries(headers)
    .map(([name, value]) => `${name}: ${value}`)
    .join("\r\n");
}

function parseHttpResponse(raw) {
  const split = raw.indexOf("\r\n\r\n");
  const head = split === -1 ? raw : raw.slice(0, split);
  const body = split === -1 ? "" : raw.slice(split + 4);
  const status = Number(head.split(" ")[1]);
  return {status, body};
}

function readAll(stream) {
  return new Promise((resolve, reject) => {
    const chunks = [];
    stream.on("data", (chunk) => chunks.push(chunk));
    stream.on("end", () => resolve(Buffer.concat(chunks).toString("utf8")));
    stream.on("error", reject);
  });
}

function getJSON(urlString, token) {
  const url = new URL(urlString);
  const headers = {Accept: "application/json", Connection: "close"};
  if (token) headers.Authorization = `Bearer ${token}`;

  if (url.protocol === "http:") {
    return new Promise((resolve, reject) => {
      const req = http.request(
        url,
        {method: "GET", headers},
        (response) => {
          readAll(response).then((body) => {
            if ((response.statusCode ?? 500) >= 400) {
              reject(new Error(`${response.statusCode} ${urlString}\n${body}`));
              return;
            }
            resolve(JSON.parse(body));
          }, reject);
        },
      );
      req.on("error", reject);
      req.end();
    });
  }

  const proxy = env("HTTPS_PROXY", "https_proxy");
  const requestText =
    `GET ${url.pathname}${url.search} HTTP/1.1\r\n` +
    `${headerLines({Host: url.host, ...headers})}\r\n\r\n`;

  if (!proxy) {
    return new Promise((resolve, reject) => {
      const socket = tls.connect(
        {host: url.hostname, port: Number(url.port || 443), servername: url.hostname},
        () => socket.write(requestText),
      );
      readAll(socket).then((raw) => {
        const {status, body} = parseHttpResponse(raw);
        if (status >= 400) reject(new Error(`${status} ${urlString}\n${body}`));
        else resolve(JSON.parse(body));
      }, reject);
    });
  }

  const proxyUrl = new URL(proxy);
  return new Promise((resolve, reject) => {
    const req = http.request({
      hostname: proxyUrl.hostname,
      port: proxyUrl.port || 80,
      method: "CONNECT",
      path: `${url.hostname}:${url.port || 443}`,
    });
    req.on("connect", (response, socket) => {
      if (response.statusCode !== 200) {
        reject(new Error(`proxy CONNECT ${response.statusCode}`));
        return;
      }
      const tlsSocket = tls.connect({socket, servername: url.hostname}, () => {
        tlsSocket.write(requestText);
      });
      readAll(tlsSocket).then((raw) => {
        const {status, body} = parseHttpResponse(raw);
        if (status >= 400) reject(new Error(`${status} ${urlString}\n${body}`));
        else resolve(JSON.parse(body));
      }, reject);
    });
    req.on("error", reject);
    req.end();
  });
}

function money(amount) {
  if (amount == null || amount === "") return "";
  const n = Number(amount);
  if (Number.isNaN(n)) return String(amount);
  return `$${n.toLocaleString("en-US")}`;
}

const {base, token} = hubspotBase();
const payload = await getJSON(`${base}/crm/v3/objects/deals`, token);
const deals = payload.results ?? [];
console.log(`Acme CRM  ·  ${deals.length} deals`);
console.log();
if (deals.length === 0) {
  console.log("  (no deals)");
  process.exit(0);
}
for (const deal of deals) {
  const props = deal.properties ?? {};
  const name = props.dealname || "(untitled deal)";
  const stage = props.dealstage || "unknown-stage";
  const invoice = props.invoice ? `  ·  ${props.invoice}` : "";
  const amount = money(props.amount);
  const amountBit = amount ? `  ·  ${amount}` : "";
  console.log(`  ${name}${amountBit}  ·  ${stage}${invoice}`);
  if (props.description) console.log(`    ${props.description}`);
  console.log();
}
