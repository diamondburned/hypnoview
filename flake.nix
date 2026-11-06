{
  description = "hypnoview rolling popularity index and web UI";

  inputs = {
    nixpkgs.url = "github:nixos/nixpkgs?ref=nixos-unstable";
    flake-parts.url = "github:hercules-ci/flake-parts";
  };

  outputs =
    inputs@{ self, flake-parts, ... }:
    flake-parts.lib.mkFlake { inherit inputs; } {
      flake.nixosModules.default = import ./nix/module.nix self;
      flake.nixosModules.hypnoview = self.nixosModules.default;

      systems = [
        "x86_64-linux"
        "aarch64-linux"
        "aarch64-darwin"
      ];

      perSystem =
        {
          self',
          pkgs,
          lib,
          ...
        }:
        {
          packages = rec {
            default = hypnoview;
            hypnoview = pkgs.buildGoModule {
              pname = "hypnoview";
              version = self.shortRev or self.dirtyShortRev or "2.0.0";

              src = lib.cleanSource ./.;

              subPackages = [ "cmd/hypnoview" ];

              vendorHash = "sha256-KmsFIXKRl39YvWy3/5xnNHWYhAN6apN7dw+h9/LTnRM=";

              checkFlags = [ "-skip=Example" ];

              meta = {
                description = "Rolling popularity index and modern web UI for Hypnohub";
                homepage = "https://libdb.so/hypnoview";
                license = lib.licenses.agpl3Only;
                mainProgram = "hypnoview";
              };
            };
          };

          devShells.default = pkgs.mkShell {
            packages = with pkgs; [
              go
              gopls
              gotools
              sqlc
              just
              reflex
              sqlite
              nixfmt
            ];
          };

          formatter = pkgs.nixfmt;

          checks = lib.optionalAttrs pkgs.stdenv.hostPlatform.isLinux {
            vm-test = pkgs.callPackage ./nix/vm-test.nix { inherit self inputs; };
          };
        };
    };
}
