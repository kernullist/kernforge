#!/usr/bin/env node

const SERVER_INFO = {
  name: "web-research",
  version: "1.0.0",
};

const DEFAULT_TIMEOUT_MS = clampInteger(process.env.WEB_RESEARCH_TIMEOUT_MS, 20000, 5000, 120000);
const DEFAULT_MAX_RESULTS = clampInteger(process.env.WEB_RESEARCH_MAX_RESULTS, 5, 1, 10);
const DEFAULT_MAX_CHARS = clampInteger(process.env.WEB_RESEARCH_MAX_CHARS, 12000, 2000, 40000);
const USER_AGENT = "kernforge-web-research-mcp/1.0";

let inputBuffer = Buffer.alloc(0);

process.stdin.on("data", (chunk) =>
{
  inputBuffer = Buffer.concat([inputBuffer, chunk]);
  drainMessages().catch((error) =>
  {
    writeStderr(`message drain failed: ${error.message}`);
  });
});

process.stdin.on("end", () =>
{
  process.exit(0);
});

function clampInteger(value, fallback, minValue, maxValue)
{
  const parsed = Number.parseInt(String(value ?? ""), 10);
  if (!Number.isFinite(parsed))
  {
    return fallback;
  }
  return Math.min(maxValue, Math.max(minValue, parsed));
}

function writeStderr(text)
{
  process.stderr.write(`${String(text).trim()}\n`);
}

function encodeMessage(payload)
{
  const body = Buffer.from(JSON.stringify(payload), "utf8");
  const header = Buffer.from(`Content-Length: ${body.length}\r\n\r\n`, "ascii");
  return Buffer.concat([header, body]);
}

function sendMessage(payload)
{
  process.stdout.write(encodeMessage(payload));
}

function sendResult(id, result)
{
  sendMessage({
    jsonrpc: "2.0",
    id,
    result,
  });
}

function sendError(id, code, message, data)
{
  sendMessage({
    jsonrpc: "2.0",
    id,
    error: {
      code,
      message,
      data,
    },
  });
}

async function drainMessages()
{
  for (;;)
  {
    const headerEnd = inputBuffer.indexOf("\r\n\r\n");
    if (headerEnd === -1)
    {
      return;
    }
    const headerText = inputBuffer.slice(0, headerEnd).toString("ascii");
    const headers = parseHeaders(headerText);
    const contentLength = Number.parseInt(headers["content-length"] ?? "", 10);
    if (!Number.isFinite(contentLength) || contentLength < 0)
    {
      throw new Error("missing Content-Length header");
    }
    const messageStart = headerEnd + 4;
    const messageEnd = messageStart + contentLength;
    if (inputBuffer.length < messageEnd)
    {
      return;
    }
    const payload = inputBuffer.slice(messageStart, messageEnd).toString("utf8");
    inputBuffer = inputBuffer.slice(messageEnd);
    let request = null;
    try
    {
      request = JSON.parse(payload);
    }
    catch (error)
    {
      sendError(null, -32700, "invalid json", { detail: error.message });
      continue;
    }
    await handleRequest(request);
  }
}

function parseHeaders(text)
{
  const headers = {};
  for (const line of text.split("\r\n"))
  {
    const separator = line.indexOf(":");
    if (separator === -1)
    {
      continue;
    }
    const key = line.slice(0, separator).trim().toLowerCase();
    const value = line.slice(separator + 1).trim();
    headers[key] = value;
  }
  return headers;
}

function emptyToolResult(text)
{
  return {
    content: [
      {
        type: "text",
        text,
      },
    ],
  };
}

function toolDefinitions()
{
  return [
    {
      name: "search_web",
      description: "Search the live web for current articles, vendor posts, and papers.",
      inputSchema: {
        type: "object",
        properties: {
          query: {
            type: "string",
            description: "Search query to send to the configured web provider.",
          },
          max_results: {
            type: "integer",
            description: "Maximum number of search results to return.",
            minimum: 1,
            maximum: 10,
          },
        },
        required: ["query"],
      },
    },
    {
      name: "fetch_url",
      description: "Fetch a URL and return readable page text for synthesis.",
      inputSchema: {
        type: "object",
        properties: {
          url: {
            type: "string",
            description: "HTTP or HTTPS URL to fetch.",
          },
          max_chars: {
            type: "integer",
            description: "Maximum number of characters to keep from the fetched page.",
            minimum: 2000,
            maximum: 40000,
          },
        },
        required: ["url"],
      },
    },
  ];
}

async function handleRequest(request)
{
  const method = String(request?.method ?? "");
  const id = Object.prototype.hasOwnProperty.call(request ?? {}, "id") ? request.id : null;
  try
  {
    switch (method)
    {
      case "initialize":
      {
        sendResult(id, {
          protocolVersion: "2024-11-05",
          capabilities: {
            tools: {},
            resources: {},
            prompts: {},
          },
          serverInfo: SERVER_INFO,
        });
        return;
      }
      case "notifications/initialized":
      {
        return;
      }
      case "tools/list":
      {
        sendResult(id, {
          tools: toolDefinitions(),
        });
        return;
      }
      case "resources/list":
      {
        sendResult(id, {
          resources: [],
        });
        return;
      }
      case "prompts/list":
      {
        sendResult(id, {
          prompts: [],
        });
        return;
      }
      case "tools/call":
      {
        const params = request?.params ?? {};
        const name = String(params?.name ?? "");
        const argumentsObject = isRecord(params?.arguments) ? params.arguments : {};
        if (name === "search_web")
        {
          const text = await searchWeb(argumentsObject);
          sendResult(id, emptyToolResult(text));
          return;
        }
        if (name === "fetch_url")
        {
          const text = await fetchUrl(argumentsObject);
          sendResult(id, emptyToolResult(text));
          return;
        }
        sendError(id, -32602, "unknown tool", { tool: name });
        return;
      }
      default:
      {
        if (id !== null)
        {
          sendError(id, -32601, "method not found", { method });
        }
        return;
      }
    }
  }
  catch (error)
  {
    if (id !== null)
    {
      sendError(id, -32000, error.message, {
        detail: error.stack,
      });
    }
  }
}

function isRecord(value)
{
  return value !== null && typeof value === "object" && !Array.isArray(value);
}

function requireString(args, key)
{
  const value = String(args?.[key] ?? "").trim();
  if (value === "")
  {
    throw new Error(`missing required string argument: ${key}`);
  }
  return value;
}

function validateHttpUrl(value)
{
  let parsed = null;
  try
  {
    parsed = new URL(value);
  }
  catch (error)
  {
    throw new Error(`invalid URL: ${value}`);
  }
  if (parsed.protocol !== "http:" && parsed.protocol !== "https:")
  {
    throw new Error(`unsupported URL scheme: ${parsed.protocol}`);
  }
  return parsed.toString();
}

function chooseSearchProvider()
{
  if (String(process.env.TAVILY_API_KEY ?? "").trim() !== "")
  {
    return {
      name: "tavily",
      run: searchWithTavily,
    };
  }
  if (String(process.env.BRAVE_SEARCH_API_KEY ?? "").trim() !== "")
  {
    return {
      name: "brave",
      run: searchWithBrave,
    };
  }
  if (String(process.env.SERPAPI_API_KEY ?? "").trim() !== "")
  {
    return {
      name: "serpapi",
      run: searchWithSerpAPI,
    };
  }
  throw new Error("missing web search API key. Set TAVILY_API_KEY, BRAVE_SEARCH_API_KEY, or SERPAPI_API_KEY before using search_web");
}

async function searchWeb(args)
{
  const query = requireString(args, "query");
  const maxResults = clampInteger(args?.max_results, DEFAULT_MAX_RESULTS, 1, 10);
  const provider = chooseSearchProvider();
  const results = await provider.run(query, maxResults);
  if (!Array.isArray(results) || results.length === 0)
  {
    return `Provider: ${provider.name}\nQuery: ${query}\nNo results returned.`;
  }
  const lines = [
    `Provider: ${provider.name}`,
    `Query: ${query}`,
    `Results: ${results.length}`,
    "",
  ];
  for (let index = 0; index < results.length; index += 1)
  {
    const item = results[index];
    lines.push(`${index + 1}. ${item.title || "(untitled)"}`);
    lines.push(`   URL: ${item.url || ""}`);
    if (item.publishedAt)
    {
      lines.push(`   Published: ${item.publishedAt}`);
    }
    if (item.source)
    {
      lines.push(`   Source: ${item.source}`);
    }
    if (item.snippet)
    {
      lines.push(`   Snippet: ${item.snippet}`);
    }
    lines.push("");
  }
  return lines.join("\n").trim();
}

async function searchWithTavily(query, maxResults)
{
  const body = await fetchJSON("https://api.tavily.com/search", {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      "User-Agent": USER_AGENT,
    },
    body: JSON.stringify({
      api_key: process.env.TAVILY_API_KEY,
      query,
      search_depth: "advanced",
      max_results: maxResults,
      include_answer: false,
      include_raw_content: false,
      include_images: false,
    }),
  });
  return (body?.results ?? []).map((item) =>
  {
    return {
      title: String(item?.title ?? "").trim(),
      url: String(item?.url ?? "").trim(),
      publishedAt: String(item?.published_date ?? "").trim(),
      snippet: compactText(String(item?.content ?? "").trim(), 320),
      source: "Tavily",
    };
  });
}

async function searchWithBrave(query, maxResults)
{
  const target = new URL("https://api.search.brave.com/res/v1/web/search");
  target.searchParams.set("q", query);
  target.searchParams.set("count", String(maxResults));
  const body = await fetchJSON(target.toString(), {
    method: "GET",
    headers: {
      "Accept": "application/json",
      "User-Agent": USER_AGENT,
      "X-Subscription-Token": process.env.BRAVE_SEARCH_API_KEY,
    },
  });
  return (body?.web?.results ?? []).map((item) =>
  {
    return {
      title: String(item?.title ?? "").trim(),
      url: String(item?.url ?? "").trim(),
      publishedAt: String(item?.age ?? "").trim(),
      snippet: compactText(String(item?.description ?? "").trim(), 320),
      source: "Brave Search",
    };
  });
}

async function searchWithSerpAPI(query, maxResults)
{
  const target = new URL("https://serpapi.com/search.json");
  target.searchParams.set("engine", "google");
  target.searchParams.set("q", query);
  target.searchParams.set("num", String(maxResults));
  target.searchParams.set("api_key", process.env.SERPAPI_API_KEY);
  const body = await fetchJSON(target.toString(), {
    method: "GET",
    headers: {
      "Accept": "application/json",
      "User-Agent": USER_AGENT,
    },
  });
  return (body?.organic_results ?? []).map((item) =>
  {
    return {
      title: String(item?.title ?? "").trim(),
      url: String(item?.link ?? "").trim(),
      publishedAt: String(item?.date ?? "").trim(),
      snippet: compactText(String(item?.snippet ?? "").trim(), 320),
      source: "SerpAPI",
    };
  });
}

async function fetchUrl(args)
{
  const target = validateHttpUrl(requireString(args, "url"));
  const maxChars = clampInteger(args?.max_chars, DEFAULT_MAX_CHARS, 2000, 40000);
  let text = "";
  let source = "";
  try
  {
    text = await fetchViaJinaReader(target);
    source = "jina-reader";
  }
  catch (error)
  {
    text = await fetchDirectPageText(target);
    source = "direct-fetch";
  }
  const compacted = compactText(text, maxChars);
  return [
    `URL: ${target}`,
    `Fetch source: ${source}`,
    `Characters: ${compacted.length}`,
    "",
    compacted,
  ].join("\n").trim();
}

async function fetchViaJinaReader(target)
{
  if (String(process.env.WEB_RESEARCH_DISABLE_JINA ?? "").trim() === "1")
  {
    throw new Error("jina reader disabled");
  }
  const trimmed = target.replace(/^https?:\/\//i, "");
  const readerURL = `https://r.jina.ai/http://${trimmed}`;
  const response = await fetchWithTimeout(readerURL, {
    method: "GET",
    headers: {
      "Accept": "text/plain, text/markdown;q=0.9, */*;q=0.1",
      "User-Agent": USER_AGENT,
    },
  });
  const text = await response.text();
  if (!response.ok)
  {
    throw new Error(`jina reader failed: ${response.status}`);
  }
  return compactText(text, DEFAULT_MAX_CHARS);
}

async function fetchDirectPageText(target)
{
  const response = await fetchWithTimeout(target, {
    method: "GET",
    headers: {
      "Accept": "text/html, text/plain;q=0.9, */*;q=0.1",
      "User-Agent": USER_AGENT,
    },
    redirect: "follow",
  });
  const body = await response.text();
  if (!response.ok)
  {
    throw new Error(`direct fetch failed: ${response.status}`);
  }
  return htmlToText(body);
}

async function fetchJSON(url, init)
{
  const response = await fetchWithTimeout(url, init);
  const text = await response.text();
  if (!response.ok)
  {
    throw new Error(`request failed: ${response.status} ${response.statusText} ${compactText(text, 300)}`);
  }
  let payload = null;
  try
  {
    payload = JSON.parse(text);
  }
  catch (error)
  {
    throw new Error(`invalid JSON response: ${error.message}`);
  }
  return payload;
}

async function fetchWithTimeout(url, init)
{
  const controller = new AbortController();
  const timeout = setTimeout(() =>
  {
    controller.abort();
  }, DEFAULT_TIMEOUT_MS);
  try
  {
    return await fetch(url, {
      ...init,
      signal: controller.signal,
    });
  }
  catch (error)
  {
    if (error?.name === "AbortError")
    {
      throw new Error(`request timed out after ${DEFAULT_TIMEOUT_MS}ms`);
    }
    throw error;
  }
  finally
  {
    clearTimeout(timeout);
  }
}

function htmlToText(input)
{
  let text = String(input ?? "");
  if (/<html[\s>]/i.test(text) || /<body[\s>]/i.test(text) || /<p[\s>]/i.test(text))
  {
    text = text.replace(/<script\b[^>]*>[\s\S]*?<\/script>/gi, " ");
    text = text.replace(/<style\b[^>]*>[\s\S]*?<\/style>/gi, " ");
    text = text.replace(/<!--[\s\S]*?-->/g, " ");
    text = text.replace(/<\/(p|div|section|article|h1|h2|h3|h4|h5|h6|li|tr|blockquote)>/gi, "\n");
    text = text.replace(/<br\s*\/?>/gi, "\n");
    text = text.replace(/<[^>]+>/g, " ");
  }
  text = decodeHtmlEntities(text);
  text = text.replace(/\r/g, "");
  text = text.replace(/[ \t]+\n/g, "\n");
  text = text.replace(/\n{3,}/g, "\n\n");
  text = text.replace(/[ \t]{2,}/g, " ");
  return text.trim();
}

function decodeHtmlEntities(input)
{
  return String(input ?? "")
    .replace(/&nbsp;/gi, " ")
    .replace(/&amp;/gi, "&")
    .replace(/&quot;/gi, "\"")
    .replace(/&#39;/gi, "'")
    .replace(/&lt;/gi, "<")
    .replace(/&gt;/gi, ">");
}

function compactText(input, maxChars)
{
  let text = String(input ?? "").trim();
  text = text.replace(/\r/g, "");
  text = text.replace(/[ \t]+\n/g, "\n");
  text = text.replace(/\n{3,}/g, "\n\n");
  text = text.replace(/[ \t]{2,}/g, " ");
  if (text.length <= maxChars)
  {
    return text;
  }
  return `${text.slice(0, Math.max(0, maxChars - 3)).trimEnd()}...`;
}
