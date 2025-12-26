<script>
    import { _ } from 'svelte-i18n';
    import CodeBlock from "@/components/base/CodeBlock.svelte";

    export let collection;

    let responseTab = 204;
    let responses = [];

    $: responses = [
        {
            code: 204,
            body: "null",
        },
        {
            code: 400,
            body: `
                {
                  "code": 400,
                  "message": "${$_("common.message.updateError")}",
                  "data": {
                    "newEmail": {
                      "code": "validation_required",
                      "message": "Missing required value."
                    }
                  }
                }
            `,
        },
        {
            code: 401,
            body: `
                {
                  "code": 401,
                  "message": "${$_("common.message.noAccess")}",
                  "data": {}
                }
            `,
        },
        {
            code: 403,
            body: `
                {
                  "code": 403,
                  "message": "${$_("common.message.insufficientAuthority")}",
                  "data": {}
                }
            `,
        },
    ];
</script>

<div class="alert alert-success">
    <strong class="label label-primary">POST</strong>
    <div class="content">
        <p>
            /api/collections/<strong>{collection.name}</strong>/request-email-change
        </p>
    </div>
    <p class="txt-hint txt-sm txt-right">Requires <code>Authorization:TOKEN</code></p>
</div>

<div class="section-title">{$_("common.placeholder.apiParameters")}</div>
<table class="table-compact table-border m-b-base">
    <thead>
        <tr>
            <th>{$_("common.placeholder.params")}</th>
            <th>{$_("common.placeholder.type")}</th>
            <th width="50%">{$_("common.placeholder.description")}</th>
        </tr>
    </thead>
    <tbody>
        <tr>
            <td>
                <div class="inline-flex">
                    <span class="label label-success">{$_("common.tip.required")}</span>
                    <span>newEmail</span>
                </div>
            </td>
            <td>
                <span class="label">String</span>
            </td>
            <td>The new email address to send the change email request.</td>
        </tr>
    </tbody>
</table>

<div class="section-title">{$_("common.placeholder.apiResponses")}</div>
<div class="tabs">
    <div class="tabs-header compact combined left">
        {#each responses as response (response.code)}
            <button
                class="tab-item"
                class:active={responseTab === response.code}
                on:click={() => (responseTab = response.code)}
            >
                {response.code}
            </button>
        {/each}
    </div>
    <div class="tabs-content">
        {#each responses as response (response.code)}
            <div class="tab-item" class:active={responseTab === response.code}>
                <CodeBlock content={response.body} />
            </div>
        {/each}
    </div>
</div>
