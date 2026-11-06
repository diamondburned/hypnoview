{
  pkgs,
  self,
  inputs,
}:

pkgs.testers.runNixOSTest {
  name = "hypnogaze";

  nodes.machine =
    { config, pkgs, ... }:
    {
      imports = [ self.nixosModules.default ];

      services.hypnogaze = {
        enable = true;
        enablePoller = false;
        minPosts = 0;
      };

      environment.systemPackages = [
        pkgs.curl
      ];

      documentation.enable = false;
    };

  testScript = ''
    start_all()
    machine.wait_for_unit("hypnogaze.service")
    machine.wait_for_open_port(8080)

    # 1. http://127.0.0.1:8080/ returns valid HTML containing hypnogaze, Browse, and Popular
    browse_html = machine.succeed("curl -fsS http://127.0.0.1:8080/")
    assert "hypnogaze" in browse_html.lower(), f"hypnogaze not in response: {browse_html[:300]}"
    assert "Browse" in browse_html, f"Browse not in response: {browse_html[:300]}"
    assert "Popular" in browse_html, f"Popular not in response: {browse_html[:300]}"

    # 2. http://127.0.0.1:8080/popular returns 200 OK HTML
    popular_html = machine.succeed("curl -fsS http://127.0.0.1:8080/popular")
    assert "hypnogaze" in popular_html.lower(), f"hypnogaze not in response: {popular_html[:300]}"

    # 3. http://127.0.0.1:8080/api/v1/stats returns valid JSON ("total_posts": 0)
    import json
    stats_raw = machine.succeed("curl -fsS http://127.0.0.1:8080/api/v1/stats")
    stats = json.loads(stats_raw)
    assert stats.get("total_posts") == 0, f"unexpected total_posts: {stats}"

    # 4. PWA endpoints: /manifest.webmanifest, /sw.js, and /shell.html
    manifest_raw = machine.succeed("curl -fsS http://127.0.0.1:8080/manifest.webmanifest")
    manifest = json.loads(manifest_raw)
    assert manifest.get("name") == "hypnogaze", f"unexpected manifest: {manifest}"
    assert manifest.get("display") == "standalone", f"unexpected display: {manifest}"

    sw_raw = machine.succeed("curl -fsS http://127.0.0.1:8080/sw.js")
    assert "hypnogaze-v-" in sw_raw, f"sw.js missing content-hash cache version: {sw_raw[:200]}"
    assert "__CACHE_VERSION__" not in sw_raw, "sw.js contains unresolved __CACHE_VERSION__"

    shell_html = machine.succeed("curl -fsS http://127.0.0.1:8080/static/shell.html")
    assert "<svg" not in shell_html, "shell.html contains inline <svg>"
    assert "pwa-shell-stage" in shell_html, "shell.html missing pwa-shell-stage"

    # 5. /var/lib/hypnogaze/hypnogaze.db exists on disk inside the VM
    machine.succeed("test -f /var/lib/hypnogaze/hypnogaze.db")
  '';
}
