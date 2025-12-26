<script>
    import { _ } from 'svelte-i18n';
    import CodeBlock from "@/components/base/CodeBlock.svelte";
    import CommonHelper from "@/utils/CommonHelper";

    export let collection;

    let responseTab = 200;
    let responses = [];

    $: responses = [
        {
            code: 200,
            body: JSON.stringify(
                {
                    otpId: CommonHelper.randomString(15),
                },
                null,
                2,
            ),
        },
        {
            code: 400,
            body: `
                {
                  "code": 400,
                  "message": "${$_("common.message.updateError")}",
                  "data": {
                    "email": {
                      "code": "validation_is_email",
                      "message": "Must be a valid email address."
                    }
                  }
                }
            `,
        },
        {
            code: 429,
            body: `
                {
                  "code": 429,
                  "message": "You've send too many OTP requests, please try again later.",
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
            /api/collections/<strong>{collection.name}</strong>/request-otp
        </p>
    </div>
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
                    <span>email</span>
                </div>
            </td>
            <td>
                <span class="label">String</span>
            </td>
            <td>The auth record email address to send the OTP request (if exists).</td>
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
