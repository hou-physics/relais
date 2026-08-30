package cli

import "fmt"

func RunDraft(args []string) error {
	o, err := prepareOutgoing(args, "draft")
	if err != nil {
		return err
	}
	d, err := o.client.CreateDraft(o.proj.Channel, o.req)
	if err != nil {
		return o.failWithDraft(fmt.Errorf("提交草稿失败: %w", err))
	}
	fmt.Printf("草稿已提交（id %s，收件人 %v）\n请到网页 %s 的频道 %q 里确认后发送。\n",
		d.ID, d.To, o.cfg.Server, o.proj.Channel)
	return nil
}
