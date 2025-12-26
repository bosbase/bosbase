<script>
    import { _ } from 'svelte-i18n';
    import CodeBlock from "@/components/base/CodeBlock.svelte";
    import SdkTabs from "@/components/base/SdkTabs.svelte";
    import ApiClient from "@/utils/ApiClient";
    import CommonHelper from "@/utils/CommonHelper";

    export let collection;

    $: backendAbsUrl = CommonHelper.getApiExampleUrl(ApiClient.baseURL);
</script>

<h3 class="m-b-sm">
    {$_("common.popup.apiDocs.sseApi.name")}
</h3>
<div class="content txt-lg m-b-sm">
    <p>{$_("common.popup.apiDocs.sseApi.content.1", { values: { tableName: collection.name } })}</p>
    <p>
        Events are sent for <strong>create</strong>, <strong>update</strong>
        and <strong>delete</strong> record operations (see "Event data format" section below).
    </p>
</div>

<div class="alert alert-info m-t-10 m-b-sm">
    <div class="icon">
        <i class="ri-information-line" />
    </div>
    <div class="contet">
        <p>
            <strong>You could subscribe to a single record or to an entire collection.</strong>
        </p>
        <p>
            When you subscribe to a <strong>single record</strong>, the collection's
            <strong>ViewRule</strong> will be used to determine whether the subscriber has access to receive the
            event message.
        </p>
        <p>
            When you subscribe to an <strong>entire collection</strong>, the collection's
            <strong>ListRule</strong> will be used to determine whether the subscriber has access to receive the
            event message.
        </p>
    </div>
</div>

<SdkTabs
    js={`
        import BosBase from 'bosbase';

        const pb = new BosBase('${backendAbsUrl}');

        ...

        // (Optionally) authenticate
        await pb.collection('users').authWithPassword('test@example.com', '123456');

        // Subscribe to changes in any ${collection?.name} record
        pb.collection('${collection?.name}').subscribe('*', function (e) {
            console.log(e.action);
            console.log(e.record);
        }, { /* other options like: filter, expand, custom headers, etc. */ });

        // Subscribe to changes only in the specified record
        pb.collection('${collection?.name}').subscribe('RECORD_ID', function (e) {
            console.log(e.action);
            console.log(e.record);
        }, { /* other options like: filter, expand, custom headers, etc. */ });

        // Unsubscribe
        pb.collection('${collection?.name}').unsubscribe('RECORD_ID'); // remove all 'RECORD_ID' subscriptions
        pb.collection('${collection?.name}').unsubscribe('*'); // remove all '*' topic subscriptions
        pb.collection('${collection?.name}').unsubscribe(); // remove all subscriptions in the collection
    `}
    dart={`
        import 'package:bosbase/bosbase.dart';

        final pb = BosBase('${backendAbsUrl}');

        ...

        // (Optionally) authenticate
        await pb.collection('users').authWithPassword('test@example.com', '123456');

        // Subscribe to changes in any ${collection?.name} record
        pb.collection('${collection?.name}').subscribe('*', (e) {
            print(e.action);
            print(e.record);
        }, /* other options like: filter, expand, custom headers, etc. */);

        // Subscribe to changes only in the specified record
        pb.collection('${collection?.name}').subscribe('RECORD_ID', (e) {
            print(e.action);
            print(e.record);
        }, /* other options like: filter, expand, custom headers, etc. */);

        // Unsubscribe
        pb.collection('${collection?.name}').unsubscribe('RECORD_ID'); // remove all 'RECORD_ID' subscriptions
        pb.collection('${collection?.name}').unsubscribe('*'); // remove all '*' topic subscriptions
        pb.collection('${collection?.name}').unsubscribe(); // remove all subscriptions in the collection
    `}
/>

<h6 class="m-b-xs">{$_("common.placeholder.apiUrl")}</h6>
<div class="alert">
    <strong class="label label-primary">SSE</strong>
    <div class="content">
        <p>/api/realtime</p>
    </div>
</div>

<div class="section-title">{$_("common.placeholder.eventDataFormat")}</div>
<CodeBlock
    content={JSON.stringify(
        {
            action: "create",
            record: CommonHelper.dummyCollectionRecord(collection),
        },
        null,
        2,
    ).replace('"action": "create"', '"action": "create" // create, update or delete')}
/>
