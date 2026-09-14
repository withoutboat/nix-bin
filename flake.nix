{
  description = "Custom CLI utilities and daemons for NixOS";

  inputs = {
    nixpkgs.url = "github:nixos/nixpkgs/nixos-unstable";
  };

  outputs = { self, nixpkgs }:
    let
      supportedSystems = [ "x86_64-linux" "aarch64-linux" ];
      forAllSystems = f: nixpkgs.lib.genAttrs supportedSystems (system: f {
        pkgs = import nixpkgs { inherit system; };
      });
    in
    {
      packages = forAllSystems ({ pkgs }: rec {
        vial-daemon = pkgs.rustPlatform.buildRustPackage {
          pname = "vial-daemon";
          version = "0.1.0";

          src = ./rust;

          cargoLock = {
            lockFile = ./rust/Cargo.lock;
          };

          meta = with pkgs.lib; {
            description = "Lightweight background daemon for tracking active keyboard layer on W-Corne DH747";
            homepage = "https://github.com/withoutboat/nix-bin";
            license = licenses.mit;
            maintainers = [ ];
            mainProgram = "vial-daemon";
            platforms = platforms.linux;
          };
        };

        projector = pkgs.buildGoModule {
          pname = "projector";
          version = "0.1.0";

          src = ./go;

          vendorHash = null;

          subPackages = [ "cmd/projector" ];

          meta = with pkgs.lib; {
            description = "CLI tool to synchronize git repositories based on a projects YAML configuration";
            homepage = "https://github.com/withoutboat/nix-bin";
            license = licenses.mit;
            maintainers = [ ];
            mainProgram = "projector";
            platforms = platforms.linux;
          };
        };

        default = vial-daemon;
      });

      devShells = forAllSystems ({ pkgs }: {
        default = pkgs.mkShell {
          packages = with pkgs; [
            cargo
            rustc
            clippy
            rustfmt
            go
            gopls
          ];
        };
      });
    };
}
