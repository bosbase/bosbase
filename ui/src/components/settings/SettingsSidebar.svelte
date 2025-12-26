<script>
    import { _ } from 'svelte-i18n';
    import PageSidebar from "@/components/base/PageSidebar.svelte";
    import { hideControls } from "@/stores/app";
    import { link } from "svelte-spa-router";
    import active from "svelte-spa-router/active";
</script>

<PageSidebar class="settings-sidebar">
    <div class="sidebar-content">
        <div class="sidebar-title">{$_("common.menu.setting")}</div>
        <a href="/settings" class="sidebar-list-item" use:active={{ path: "/settings" }} use:link>
            <i class="ri-home-gear-line" aria-hidden="true" />
            <span class="txt">{$_("common.menu.appConfig")}</span>
        </a>
        <a
            href="/settings/mail"
            class="sidebar-list-item"
            use:active={{ path: "/settings/mail/?.*" }}
            use:link
        >
            <i class="ri-send-plane-2-line" aria-hidden="true" />
            <span class="txt">{$_("common.menu.smtpConfig")}</span>
        </a>
        <a
            href="/settings/storage"
            class="sidebar-list-item"
            use:active={{ path: "/settings/storage/?.*" }}
            use:link
        >
            <i class="ri-archive-drawer-line" aria-hidden="true" />
            <span class="txt">{$_("common.menu.storageConfig")}</span>
        </a>
        <a
            href="/settings/backups"
            class="sidebar-list-item"
            use:active={{ path: "/settings/backups/?.*" }}
            use:link
        >
            <i class="ri-archive-line" aria-hidden="true" />
            <span class="txt">{$_("common.menu.backupConfig")}</span>
        </a>

        {#if !$hideControls}
            <div class="sidebar-title">
                <span class="txt">{$_("common.subfield.syncSetting")}</span>
            </div>
            <a
                href="/settings/export-collections"
                class="sidebar-list-item"
                use:active={{ path: "/settings/export-collections/?.*" }}
                use:link
            >
                <i class="ri-uninstall-line" aria-hidden="true" />
                <span class="txt">{$_("common.action.export")}</span>
            </a>
            <a
                href="/settings/import-collections"
                class="sidebar-list-item"
                use:active={{ path: "/settings/import-collections/?.*" }}
                use:link
            >
                <i class="ri-install-line" aria-hidden="true" />
                <span class="txt">{$_("common.action.import")}</span>
            </a>
        {/if}
    </div>
</PageSidebar>
