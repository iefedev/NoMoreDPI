using System;
using System.IO;
using System.IO.Pipes;
using System.Text;
using System.Text.Json;
using System.Text.Json.Nodes;
using System.Threading;
using System.Threading.Tasks;

namespace NoMoreDPI;

/// <summary>Client for the service's named pipe (internal/ipc): one JSON request line,
/// one JSON response line, a fresh connection per call like ipc.Call.</summary>
public static class Pipe
{
    const string Name = "nomoredpi";

    public static async Task<JsonNode?> CallAsync(string method, JsonNode? param = null, TimeSpan? timeout = null)
    {
#if DEBUG
        if (Demo.Enabled) return await Demo.CallAsync(method, param);
#endif
        var limit = timeout ?? TimeSpan.FromSeconds(15);
        using var cts = new CancellationTokenSource(limit);
        try
        {
            await using var pipe = new NamedPipeClientStream(".", Name, PipeDirection.InOut, PipeOptions.Asynchronous);
            // .NET keeps retrying a pipe that does not exist (go-winio fails at once), so
            // the connect gets its own short limit: a stopped service must show quickly.
            using (var connect = CancellationTokenSource.CreateLinkedTokenSource(cts.Token))
            {
                connect.CancelAfter(TimeSpan.FromSeconds(2));
                try { await pipe.ConnectAsync(connect.Token); }
                catch (OperationCanceledException) { throw new IOException("The No More DPI service is not running."); }
            }
            var req = new JsonObject { ["method"] = method };
            if (param != null) req["params"] = param.DeepClone();
            var line = Encoding.UTF8.GetBytes(req.ToJsonString() + "\n");
            await pipe.WriteAsync(line, cts.Token);
            await pipe.FlushAsync(cts.Token);
            using var reader = new StreamReader(pipe, new UTF8Encoding(false));
            var resp = await reader.ReadLineAsync(cts.Token) ?? throw new IOException("The service did not answer.");
            var node = JsonNode.Parse(resp) as JsonObject ?? throw new IOException("The service sent a malformed answer.");
            if (node["ok"]?.GetValue<bool>() != true)
                throw new IOException(node["error"]?.GetValue<string>() ?? "unknown error");
            return node["result"];
        }
        catch (OperationCanceledException)
        {
            throw new TimeoutException("The service did not answer in time.");
        }
    }

    public static async Task<T?> CallAsync<T>(string method, JsonNode? param = null, TimeSpan? timeout = null)
    {
        var r = await CallAsync(method, param, timeout);
        return r == null ? default : r.Deserialize<T>(Json.Options);
    }
}

public static class Json
{
    public static readonly JsonSerializerOptions Options = new() { PropertyNameCaseInsensitive = true };
}
