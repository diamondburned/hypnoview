{ pkgs ? import <nixpkgs> {} }:

pkgs.mkShell {
	buildInputs = with pkgs; [
		go
		gopls
		gotools
		deno
		nodejs
	];

	shellHook = ''
		NODE_MODULES_BIN=(
			$(find "${builtins.toPath ./.}" -path '*/node_modules/.bin' \
				| sort \
				| tr $'\n' :)
		)
		export PATH="$PATH:$NODE_MODULES_BIN"
	'';
}
