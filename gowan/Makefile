include $(TOPDIR)/rules.mk

PKG_NAME:=gowan
PKG_VERSION:=0.8.0
PKG_RELEASE:=1

# Source is VENDORED in ./src — an upstream go-dispatch-proxy snapshot (MIT)
# plus GoWAN's health/state modules. Provenance in src/UPSTREAM.md.
# No download step, no PKG_MIRROR_HASH.
PKG_LICENSE:=MIT
PKG_LICENSE_FILES:=src/LICENSE
PKG_MAINTAINER:=Ali Almohaya <ali.almohaya@anghami.com>

PKG_BUILD_DEPENDS:=golang/host
PKG_BUILD_PARALLEL:=1
PKG_BUILD_FLAGS:=no-mips16

GO_PKG:=gowan
GO_PKG_LDFLAGS:=-s -w

include $(INCLUDE_DIR)/package.mk
include $(TOPDIR)/feeds/packages/lang/golang/golang-package.mk

define Build/Prepare
	mkdir -p $(PKG_BUILD_DIR)
	$(CP) ./src/* $(PKG_BUILD_DIR)/
endef

define Package/gowan
  SECTION:=net
  CATEGORY:=Network
  TITLE:=GoWAN - Multi-WAN SOCKS5 load balancer
  URL:=https://github.com/Almo7aya/luci-app-gowan
  # kmod-nft-tproxy is only needed for the optional transparent-UDP
  # feature; it is small and lets the LuCI toggle work out of the box.
  DEPENDS:=$(GO_ARCH_DEPENDS) +kmod-nft-tproxy
endef

define Package/gowan/description
  Multi-WAN SOCKS5 load balancing proxy for OpenWrt. Balances TCP
  connections across multiple WAN interfaces with native health checks,
  dial-failure fallback, weighted round-robin and nftables ACL —
  UCI-configured, zero runtime dependencies.
endef

define Package/gowan/install
	$(INSTALL_DIR) $(1)/usr/sbin
	$(INSTALL_BIN) $(GO_PKG_BUILD_BIN_DIR)/gowan $(1)/usr/sbin/gowan

	$(INSTALL_DIR) $(1)/etc/config
	$(INSTALL_CONF) ./files/gowan.config $(1)/etc/config/gowan

	$(INSTALL_DIR) $(1)/etc/init.d
	$(INSTALL_BIN) ./files/gowan.init $(1)/etc/init.d/gowan

	$(INSTALL_DIR) $(1)/etc/uci-defaults
	$(INSTALL_BIN) ./files/gowan.defaults $(1)/etc/uci-defaults/90-gowan

	$(INSTALL_DIR) $(1)/etc/hotplug.d/iface
	$(INSTALL_BIN) ./files/gowan-hotplug $(1)/etc/hotplug.d/iface/99-gowan

	$(INSTALL_DIR) $(1)/usr/lib/gowan
	$(INSTALL_DATA) ./files/gowan-functions.sh $(1)/usr/lib/gowan/functions.sh
	$(INSTALL_BIN) ./files/gowan-notify.sh $(1)/usr/lib/gowan/notify.sh
	$(INSTALL_BIN) ./files/gowan-speedtest.sh $(1)/usr/lib/gowan/speedtest.sh

	$(INSTALL_DIR) $(1)/usr/libexec/rpcd
	$(INSTALL_BIN) ./files/gowan-rpcd $(1)/usr/libexec/rpcd/gowan
endef

define Package/gowan/conffiles
/etc/config/gowan
endef

define Package/gowan/prerm
#!/bin/sh
[ -x /etc/init.d/gowan ] && /etc/init.d/gowan stop
nft delete table inet gowan 2>/dev/null
exit 0
endef

$(eval $(call BuildPackage,gowan))
