{
  description = "hypnogaze rolling popularity index and web UI";

  inputs = {
    nixpkgs.url = "github:nixos/nixpkgs?ref=nixos-unstable";
    flake-parts.url = "github:hercules-ci/flake-parts";
  };

  outputs =
    inputs@{ self, flake-parts, ... }:
    flake-parts.lib.mkFlake { inherit inputs; } {
      flake.nixosModules.default = import ./nix/module.nix self;
      flake.nixosModules.hypnogaze = self.nixosModules.default;
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
            default = hypnogaze;
            hypnogaze = pkgs.buildGoModule {
              pname = "hypnogaze";
              version = self.shortRev or self.dirtyShortRev or "2.0.0";

              src = lib.cleanSource ./.;

              subPackages = [ "cmd/hypnogaze" ];

              vendorHash = "sha256-CSZz8Sef4mlvHqMjibJRNVfFwyxZBKcejrwiZE8g8xA=";

              checkFlags = [ "-skip=Example" ];

              meta = {
                description = "Rolling popularity index and modern web UI for Hypnohub";
                homepage = "https://gaze.d14.pet";
                license = lib.licenses.gpl3Only;
                mainProgram = "hypnogaze";
              };
            };
            hypnoview = hypnogaze;
          };

          devShells.default = pkgs.mkShell {
            packages = with pkgs; [
              go
              gopls
              gotools
              go-tools
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
