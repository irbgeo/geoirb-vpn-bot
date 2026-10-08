package bot

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestScreensDoNotSayVPN: user-facing texts never call the service a VPN.
// Only names users must find in the Amnezia apps may keep the word.
func TestScreensDoNotSayVPN(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "screens.go", nil, 0)
	require.NoError(t, err)

	allowed := strings.NewReplacer(
		"AmneziaVPN", "",
		"DefaultVPN", "",
		"org.amnezia.vpn", "",
		"geoirb-vpn-bot", "", // systemd unit in an admin hint
		"НЕ должны использовать VPN", "",
	)
	ast.Inspect(file, func(n ast.Node) bool {
		_, ok := n.(*ast.ImportSpec)
		if ok {
			return false
		}
		lit, ok := n.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		text := strings.ToLower(allowed.Replace(lit.Value))
		require.NotContains(t, text, "vpn", lit.Value)
		require.NotContains(t, text, "впн", lit.Value)
		return true
	})
}

// TestImportTextDefaultVPNFileOnly: DefaultVPN has no QR import, so step 2
// sends its users to the .conf file.
func TestImportTextDefaultVPNFileOnly(t *testing.T) {
	require.Contains(t, importText, "DefaultVPN: только файл .conf")
}
