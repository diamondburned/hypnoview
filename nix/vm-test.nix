{
  pkgs,
  self,
  inputs,
}:

pkgs.testers.runNixOSTest {
  name = "hypnoview";

  nodes.machine =
    { config, pkgs, ... }:
    {
      imports = [ self.nixosModules.default ];

      services.hypnoview = {
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
    machine.wait_for_unit("hypnoview.service")
    machine.wait_for_open_port(8080)

    # 1. http://127.0.0.1:8080/ returns valid HTML containing hypnoview, Browse, and Popular
    browse_html = machine.succeed("curl -fsS http://127.0.0.1:8080/")
    assert "hypnoview" in browse_html.lower(), f"hypnoview not in response: {browse_html[:300]}"
    assert "Browse" in browse_html, f"Browse not in response: {browse_html[:300]}"
    assert "Popular" in browse_html, f"Popular not in response: {browse_html[:300]}"

    # 2. http://127.0.0.1:8080/popular returns 200 OK HTML
    popular_html = machine.succeed("curl -fsS http://127.0.0.1:8080/popular")
    assert "hypnoview" in popular_html, f"hypnoview not in response: {popular_html[:300]}"

    # 3. http://127.0.0.1:8080/api/v1/stats returns valid JSON ("total_posts": 0)
    import json
    stats_raw = machine.succeed("curl -fsS http://127.0.0.1:8080/api/v1/stats")
    stats = json.loads(stats_raw)
    assert stats.get("total_posts") == 0, f"unexpected total_posts: {stats}"

    # 4. /var/lib/hypnoview/hypnoview.db exists on disk inside the VM
    machine.succeed("test -f /var/lib/hypnoview/hypnoview.db")
  '';
}
