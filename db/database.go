package db

import (
	"fmt"
	"github.com/Mrs4s/go-cqhttp/global"
	"hash/crc32"
)

type (
	// IDatabase 数据库操作接口定义
	IDatabase interface {
		// Open 初始化数据库
		Open() error

		// GetMessageByGlobalID 通过 GlobalID 来获取消息
		GetMessageByGlobalID(int32) (IStoredMessage, error)
		// GetGroupMessageByGlobalID 通过 GlobalID 来获取群消息
		GetGroupMessageByGlobalID(int32) (*StoredGroupMessage, error)
		// GetPrivateMessageByGlobalID 通过 GlobalID 来获取私聊消息
		GetPrivateMessageByGlobalID(int32) (*StoredPrivateMessage, error)

		// InsertGroupMessage 向数据库写入新的群消息
		InsertGroupMessage(*StoredGroupMessage) error
		// InsertPrivateMessage 向数据库写入新的私聊消息
		InsertPrivateMessage(*StoredPrivateMessage) error
	}

	IStoredMessage interface {
		GetID() string
		GetType() string
		GetGlobalID() int32
		GetAttribute() *StoredMessageAttribute
		GetContent() []global.MSG
	}

	// StoredGroupMessage 持久化群消息
	StoredGroupMessage struct {
		ID          string                  `bson:"_id"`
		GlobalID    int32                   `bson:"globalId"`
		Attribute   *StoredMessageAttribute `bson:"attribute"`
		SubType     string                  `bson:"subType"`
		QuotedInfo  *QuotedInfo             `bson:"quotedInfo"`
		GroupCode   int64                   `bson:"groupCode"`
		AnonymousID string                  `bson:"anonymousId"`
		Content     []global.MSG            `bson:"content"`
	}

	// StoredPrivateMessage 持久化私聊消息
	StoredPrivateMessage struct {
		ID         string                  `bson:"_id"`
		GlobalID   int32                   `bson:"globalId"`
		Attribute  *StoredMessageAttribute `bson:"attribute"`
		SubType    string                  `bson:"subType"`
		QuotedInfo *QuotedInfo             `bson:"quotedInfo"`
		SessionUin int64                   `bson:"sessionUin"`
		TargetUin  int64                   `bson:"targetUin"`
		Content    []global.MSG            `bson:"content"`
	}

	// StoredMessageAttribute 持久化消息属性
	StoredMessageAttribute struct {
		MessageSeq int32  `bson:"messageSeq"`
		InternalID int32  `bson:"internalId"`
		SenderUin  int64  `bson:"senderUin"`
		SenderName string `bson:"senderName"`
		Timestamp  int64  `bson:"timestamp"`
	}

	// QuotedInfo 引用回复
	QuotedInfo struct {
		PrevID        string       `bson:"prevId"`
		PrevGlobalID  int32        `bson:"prevGlobalId"`
		QuotedContent []global.MSG `bson:"quotedContent"`
	}
)

// ToGlobalID 构建`code`-`msgID`的字符串并返回其CRC32 Checksum的值
func ToGlobalID(code int64, msgID int32) int32 {
	return int32(crc32.ChecksumIEEE([]byte(fmt.Sprintf("%d-%d", code, msgID))))
}

func (m *StoredGroupMessage) GetID() string { return m.ID }

func (m *StoredGroupMessage) GetType() string { return "group" }

func (m *StoredGroupMessage) GetGlobalID() int32 { return m.GlobalID }

func (m *StoredGroupMessage) GetAttribute() *StoredMessageAttribute { return m.Attribute }

func (m *StoredGroupMessage) GetContent() []global.MSG { return m.Content }

func (m *StoredPrivateMessage) GetID() string { return m.ID }

func (m *StoredPrivateMessage) GetType() string { return "private" }

func (m *StoredPrivateMessage) GetGlobalID() int32 { return m.GlobalID }

func (m *StoredPrivateMessage) GetAttribute() *StoredMessageAttribute { return m.Attribute }

func (m *StoredPrivateMessage) GetContent() []global.MSG { return m.Content }
